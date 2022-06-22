package bench

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/isucon/isucandar"
	isuports "github.com/isucon/isucon12-qualify/webapp/go"
)

// ベンチ実行後の整合性検証シナリオ
// isucandar.ValidateScenarioを満たすメソッド
// isucandar.Benchmark の validation ステップで実行される
func (sc *Scenario) ValidationScenario(ctx context.Context, step *isucandar.BenchmarkStep) error {
	report := timeReporter("validation")
	defer report()

	ContestantLogger.Println("[ValidationScenario] 整合性チェックを開始します")
	defer ContestantLogger.Printf("[ValidationScenario] 整合性チェックを終了します")

	// 検証で作成する参加者数 結果のScoreも同数作成する
	playerNum := 20

	// エラーチェック用のプレイヤー
	disqualifiedPlayerIndex := playerNum - 2 // 失格プレイヤー
	noScorePlayerIndex := playerNum - 1      // スコア未登録プレイヤー この値以降にはスコアは登録されない

	// SaaS管理者のagent作成
	_, adminAg, err := sc.GetAccountAndAgent(AccountRoleAdmin, "admin", "admin")
	if err != nil {
		return err
	}

	// SaaS管理API
	tenantName := "valid-tenantid"
	tenantDisplayName := "valid-Tenantname"
	{
		res, err := PostAdminTenantsAddAction(ctx, tenantName, tenantDisplayName, adminAg)
		v := ValidateResponse("新規テナント作成", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPITenantsAdd) error {
				if tenantDisplayName != r.Data.Tenant.DisplayName {
					return fmt.Errorf("作成したテナントのDisplayNameが違います (want: %s, got: %s)", tenantDisplayName, r.Data.Tenant.DisplayName)
				}
				if tenantName != r.Data.Tenant.Name {
					return fmt.Errorf("作成したテナントのNameが違います (want: %s, got: %s)", tenantName, r.Data.Tenant.Name)
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}

		// テナント追加 不正リクエストチェック
		invalidNames := map[string]int{
			"valid-tenantid":   http.StatusBadRequest, // 重複するname
			"INVALID_TENANTID": http.StatusBadRequest, // 不正なname
		}
		for name, code := range invalidNames {
			res, err := PostAdminTenantsAddAction(ctx, name, tenantDisplayName, adminAg)
			v := ValidateResponse("新規テナント作成 不正リクエスト", step, res, err, WithStatusCode(code))
			if !v.IsEmpty() {
				return v
			}
		}
	}

	// 大会主催者API
	_, orgAg, err := sc.GetAccountAndAgent(AccountRoleOrganizer, tenantName, "organizer")
	if err != nil {
		return err
	}

	// プレイヤー追加
	var playerIDs []string
	var playerDisplayNames []string
	for i := 0; i < playerNum; i++ {
		playerDisplayNames = append(playerDisplayNames, fmt.Sprintf("validate_player%d", i))
	}
	{
		res, err := PostOrganizerPlayersAddAction(ctx, playerDisplayNames, orgAg)
		v := ValidateResponse("テナントへプレイヤー追加", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPIPlayersAdd) error {
				if playerNum != len(r.Data.Players) {
					return fmt.Errorf("追加されたプレイヤー数が違います (want: %d, got: %d)", playerNum, len(r.Data.Players))
				}
				for _, pl := range r.Data.Players {
					if pl.IsDisqualified {
						return fmt.Errorf("新規追加されたプレイヤーは失格になっていない必要があります player.id: %s", pl.ID)
					}
					var ok bool
					for _, n := range playerDisplayNames {
						if n == pl.DisplayName {
							ok = true
							break
						}
					}
					if !ok {
						return fmt.Errorf("新規追加したプレイヤーのDisplayNameが見つかりません")
					}
					playerIDs = append(playerIDs, pl.ID)
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
		// NOTE: 不正リクエストチェックなし
	}

	// プレイヤー一覧取得
	{
		res, err := GetOrganizerPlayersListAction(ctx, orgAg)
		v := ValidateResponse("テナントのプレイヤー一覧取得", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPIPlayersList) error {
				if len(playerIDs) != len(r.Data.Players) {
					return fmt.Errorf("プレイヤー数が違います (want: %d, got: %d)", playerNum, len(r.Data.Players))
				}

				mapResponsePlayerID := map[string]struct{}{}
				for _, pl := range r.Data.Players {
					if pl.IsDisqualified {
						return fmt.Errorf("新規追加されたプレイヤーは失格になっていない必要があります player.id: %s", pl.ID)
					}
					mapResponsePlayerID[pl.ID] = struct{}{}
				}

				var ok bool
				for _, pid := range playerIDs {
					if _, ok = mapResponsePlayerID[pid]; !ok {
						return fmt.Errorf("新規追加されたプレイヤーが一覧に含まれていません: id:%s", pid)
					}
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
		// NOTE: 不正リクエストチェックなし
	}

	competitionTitle := "validate_competition"
	var competitionID string
	{
		res, err := PostOrganizerCompetitionsAddAction(ctx, competitionTitle, orgAg)
		v := ValidateResponse("新規大会追加", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPICompetitionsAdd) error {
				if competitionTitle != r.Data.Competition.Title {
					return fmt.Errorf("追加された大会の名前が違います (want: %s, got: %s)", competitionTitle, r.Data.Competition.Title)
				}
				if r.Data.Competition.IsFinished {
					return fmt.Errorf("新規追加された大会は開催中である必要があります competition.title: %s, competition.id: %s", r.Data.Competition.Title, r.Data.Competition.ID)
				}
				competitionID = r.Data.Competition.ID
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
		// NOTE: 不正リクエストチェックなし
	}
	{
		idx := disqualifiedPlayerIndex
		res, err := PostOrganizerApiPlayerDisqualifiedAction(ctx, playerIDs[idx], orgAg)
		v := ValidateResponse("プレイヤーを失格にする", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPIPlayerDisqualified) error {
				if !r.Data.Player.IsDisqualified {
					return fmt.Errorf("プレイヤーが失格になっていません player.id: %s", r.Data.Player.ID)
				}
				if playerIDs[idx] != r.Data.Player.ID {
					return fmt.Errorf("失格にしたプレイヤーが違います (want: %s, got: %s)", playerIDs[idx], r.Data.Player.ID)
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
	}

	// 不正リクエスト: 存在しないプレイヤーを失格にする
	{
		res, err := PostOrganizerApiPlayerDisqualifiedAction(ctx, "non-exist-player", orgAg)
		v := ValidateResponse("プレイヤーを失格にする: 不正リクエスト(存在しないプレイヤー)", step, res, err, WithStatusCode(404))
		if !v.IsEmpty() {
			return v
		}
	}

	// 大会結果CSV入稿
	var score ScoreRows
	{
		// NOTE:
		//	失格済みのプレイヤーは含まれていても問題ない
		// 	最後の一人はスコア未登録+ranking参照済みユーザーとしてbilling検証に利用する
		// 後ろのプレイヤーはスコア未登録(noScorePlayerIndex以降)
		for i, playerID := range playerIDs[:noScorePlayerIndex] {
			score = append(score, &ScoreRow{
				PlayerID: playerID,
				Score:    100 + i,
			})
		}
		csv := score.CSV()
		res, err := PostOrganizerCompetitionResultAction(ctx, competitionID, []byte(csv), orgAg)
		v := ValidateResponse("大会結果CSV入稿", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPICompetitionResult) error {
				_ = r // responseは空
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}

		// 不正リクエストチェック
		// 存在しない大会
		res, err = PostOrganizerCompetitionResultAction(ctx, "nonexisting-competition", []byte(csv), orgAg)
		v = ValidateResponse("大会結果CSV入稿: 不正リクエスト(存在しない大会)", step, res, err, WithStatusCode(404))
		if !v.IsEmpty() {
			return v
		}

		// 存在しないプレイヤーが含まれるCSVを入稿
		invalidScore := ScoreRows{&ScoreRow{
			PlayerID: "not-exist-player",
			Score:    1,
		}}
		invalidCSV := invalidScore.CSV()
		res, err = PostOrganizerCompetitionResultAction(ctx, competitionID, []byte(invalidCSV), orgAg)
		v = ValidateResponse("大会結果CSV入稿: 不正リクエスト(存在しないプレイヤー)", step, res, err, WithStatusCode(400))
		if !v.IsEmpty() {
			return v
		}
	}
	// 大会参加者API
	_, playerAg, err := sc.GetAccountAndAgent(AccountRolePlayer, tenantName, playerIDs[0])
	if err != nil {
		return err
	}

	{
		checkPlayerIndex := 10 // < disqualifiedPlayerIndex
		res, err := GetPlayerAction(ctx, playerIDs[checkPlayerIndex], playerAg)
		v := ValidateResponse("プレイヤーと戦績情報取得", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPIPlayer) error {
				if playerIDs[checkPlayerIndex] != r.Data.Player.ID {
					return fmt.Errorf("参照したプレイヤー名が違います (want: %s, got: %s)", playerIDs[checkPlayerIndex], r.Data.Player.ID)
				}
				if 1 != len(r.Data.Scores) {
					return fmt.Errorf("参加した大会数が違います (want: %d, got: %d)", 1, len(r.Data.Scores))
				}
				if competitionTitle != r.Data.Scores[0].CompetitionTitle {
					return fmt.Errorf("参加した大会IDが違います (want: %s, got: %s)", competitionTitle, r.Data.Scores[checkPlayerIndex].CompetitionTitle)
				}
				if int64(100+checkPlayerIndex) != r.Data.Scores[0].Score {
					return fmt.Errorf("スコアが違います (want: %d, got: %d)", 100+checkPlayerIndex, r.Data.Scores[0].Score)
				}

				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
	}

	// 不正リクエストチェック
	// 存在しないプレイヤー
	{
		res, err := GetPlayerAction(ctx, "not-exist-player", playerAg)
		v := ValidateResponse("プレイヤーと戦績情報取得", step, res, err, WithStatusCode(404))
		if !v.IsEmpty() {
			return v
		}
	}

	// ランキング確認
	// NOTE: 最終的なランキングが正しいことは大会終了APIを叩いた後に確認
	{
		//rank_after未指定
		res, err := GetPlayerCompetitionRankingAction(ctx, competitionID, "", playerAg)
		v := ValidateResponse("大会内のランキング取得: ページングなし", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPICompetitionRanking) error {
				if len(score) != len(r.Data.Ranks) && 100 < len(r.Data.Ranks) {
					return fmt.Errorf("大会のランキングの結果の数が違います(最大100件) (want: %d, got: %d)", len(score), len(r.Data.Ranks))
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
	}
	{
		// rank_afterで最後の1件だけを取るように指定する
		res, err := GetPlayerCompetitionRankingAction(ctx, competitionID, strconv.Itoa(len(score)-1), playerAg)
		v := ValidateResponse("大会内のランキング取得: ページングあり", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPICompetitionRanking) error {
				if 1 != len(r.Data.Ranks) {
					return fmt.Errorf("rank_after指定時の大会のランキングの結果の数が違います (want: %d, got: %d)", 1, len(r.Data.Ranks))
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
	}
	// 不正リクエストチェック
	// 存在しない大会
	{
		res, err := GetPlayerCompetitionRankingAction(ctx, "nonexisting-competition", "", playerAg)
		v := ValidateResponse("大会内のランキング取得", step, res, err, WithStatusCode(404))
		if !v.IsEmpty() {
			return v
		}
	}

	// TODO: ランキングの結果が最大100件なことを確認

	// 失格者がランキングを参照しようとする
	{
		idx := disqualifiedPlayerIndex
		_, disqualifiedPlayerAg, err := sc.GetAccountAndAgent(AccountRolePlayer, tenantName, playerIDs[idx])
		if err != nil {
			return err
		}

		res, err := GetPlayerCompetitionRankingAction(ctx, competitionID, "", disqualifiedPlayerAg)
		v := ValidateResponse("大会内のランキング取得: 失格済みプレイヤー", step, res, err, WithStatusCode(403))
		if !v.IsEmpty() {
			return v
		}
	}
	// スコア未登録プレイヤーがランキングを参照する
	{
		idx := noScorePlayerIndex
		_, noScorePlayerAg, err := sc.GetAccountAndAgent(AccountRolePlayer, tenantName, playerIDs[idx])
		if err != nil {
			return err
		}

		res, err := GetPlayerCompetitionRankingAction(ctx, competitionID, "", noScorePlayerAg)
		v := ValidateResponse("大会内のランキング取得: スコア未登録プレイヤー", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPICompetitionRanking) error {
				if len(score) != len(r.Data.Ranks) && 100 < len(r.Data.Ranks) {
					return fmt.Errorf("大会のランキングの結果の数が違います(最大100件) (want: %d, got: %d)", len(score), len(r.Data.Ranks))
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
	}

	// 主催者API 大会の終了
	{
		res, err := PostOrganizerCompetitionFinishAction(ctx, competitionID, orgAg)
		v := ValidateResponse("大会終了", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPICompetitionRankingFinish) error {
				_ = r // responseは空
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
	}
	// 不正リクエストチェック
	// 存在しない大会
	{
		res, err := PostOrganizerCompetitionFinishAction(ctx, "nonexisting-competition", orgAg)
		v := ValidateResponse("大会終了: 不正リクエスト(存在しない大会)", step, res, err, WithStatusCode(404))
		if !v.IsEmpty() {
			return v
		}
	}

	// 大会の終了(organizer/competition/finish)後は反映まで1sの猶予がある
	time.Sleep(time.Second * 1)

	// 最終的なランキングが正しいことを確認
	{
		// NOTE: 失格者はランキングから除外しない
		rankingNum := len(score)
		res, err := GetPlayerCompetitionRankingAction(ctx, competitionID, "", playerAg)
		v := ValidateResponse("大会内のランキング取得: ランキングが正しいことを確認", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPICompetitionRanking) error {
				if rankingNum != len(r.Data.Ranks) && 100 < len(r.Data.Ranks) {
					return fmt.Errorf("大会のランキングの結果の数が違います(最大100件) (want: %d, got: %d)", rankingNum, len(r.Data.Ranks))
				}

				// ランキングチェック
				// playerIDsのindexの順にscoreが大きいのでランキングは逆順になる
				// playerIDs[0]は最下位 = (noScorePlayerIndex-1)位(失格1)
				playerIDScoreMap := map[string]isuports.CompetitionRank{}
				for _, rank := range r.Data.Ranks {
					playerIDScoreMap[rank.PlayerID] = rank
				}
				for index, playerID := range playerIDs[:noScorePlayerIndex] {
					rank := int64(noScorePlayerIndex - index)
					if rank != playerIDScoreMap[playerID].Rank {
						return fmt.Errorf("ランキングのランクが違います (want: %d, got: %d)", rank, playerIDScoreMap[playerID].Rank)
					}
					score := int64(100 + index)
					if score != playerIDScoreMap[playerID].Score {
						return fmt.Errorf("ランキングのスコアが違います (want: %d, got: %d)", score, playerIDScoreMap[playerID].Score)
					}
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
	}

	// 主催者API テナント内請求情報確認
	{
		res, err := GetOrganizerBillingAction(ctx, orgAg)
		v := ValidateResponse("テナント内の請求情報", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPIBilling) error {
				if 1 != len(r.Data.Reports) {
					return fmt.Errorf("請求レポートの数が違います (want: %d, got: %d)", 1, len(r.Data.Reports))
				}
				if competitionID != r.Data.Reports[0].CompetitionID {
					return fmt.Errorf("対象の大会のIDが違います (want: %s, got: %s)", competitionID, r.Data.Reports[0].CompetitionID)
				}
				// score登録者 rankingアクセスあり: 100 yen x 1 player
				// score登録者 rankingアクセスなし:  50 yen x (playerNum - 2) player
				// score未登録者 rankingアクセスあり:  10 yen x 1 player
				billingYen := int64((100 * 1) + (50 * (playerNum - 2)) + (10 * 1))
				if billingYen != r.Data.Reports[0].BillingYen {
					return fmt.Errorf("大会の請求金額が違います competitionID: %s (want: %d, got: %d)", competitionID, billingYen, r.Data.Reports[0].BillingYen)
				}

				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
		// NOTE: 不正リクエストチェックなし
	}

	{
		res, err := GetPlayerCompetitionsAction(ctx, playerAg)
		v := ValidateResponse("テナント内の大会情報取得", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPICompetitions) error {
				if 1 != len(r.Data.Competitions) {
					return fmt.Errorf("テナントに含まれる大会の数が違います (want: %d, got: %d)", 1, len(r.Data.Competitions))
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
		// NOTE: 不正なリクエストチェックはなし
	}

	{
		// ページング無しで今回操作したテナントが含まれていることを確認
		res, err := GetAdminTenantsBillingAction(ctx, "", adminAg)
		v := ValidateResponse("テナント別の請求ダッシュボード", step, res, err, WithStatusCode(200),
			WithSuccessResponse(func(r ResponseAPITenantsBilling) error {
				if 1 > len(r.Data.Tenants) {
					return fmt.Errorf("請求ダッシュボードの結果がありません")
				}
				tenantNameMap := make(map[string]struct{})
				for _, tenant := range r.Data.Tenants {
					tenantNameMap[tenant.DisplayName] = struct{}{}
				}
				if _, ok := tenantNameMap[tenantDisplayName]; !ok {
					return fmt.Errorf("請求ダッシュボードの結果に作成したテナントがありません")
				}
				return nil
			}),
		)
		if !v.IsEmpty() {
			return v
		}
	}

	// TODO: invalid JWT
	// exp切れ

	return nil
}
