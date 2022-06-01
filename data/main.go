package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/isucon/isucon12-qualify/bench"
	"github.com/jaswdr/faker"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	_ "github.com/samber/lo"

	isuports "github.com/isucon/isucon12-qualify/webapp/go"
)

var fake = faker.New()

var epoch = time.Date(2022, 05, 01, 0, 0, 0, 0, time.UTC)  // サービス開始時点(IDの起点)
var now = time.Date(2022, 05, 31, 23, 59, 59, 0, time.UTC) // 初期データの終点
var playersNumByTenant = 1000                              // テナントごとのplayer数
var competitionsNumByTenant = 100                          // テナントごとの大会数
var disqualifiedRate = 10                                  // player失格確率
var visitsByCompetition = 30                               // 1大会のplayerごとの訪問数

var tenantDBSchemaFilePath = "../webapp/sql/tenant/10_schema.sql"
var adminDBSchemaFilePath = "../webapp/sql/admin/10_schema.sql"

func init() {
	os.Setenv("TZ", "UTC")
}

func main() {
	flag.Parse()
	tenantsNum, err := strconv.Atoi(flag.Args()[0])
	if err != nil {
		log.Fatal(err)
	}
	log.Println("tenantsNum", tenantsNum)
	log.Println("epoch", epoch)

	cmd := exec.Command("sh", "-c", fmt.Sprintf("mysql -uisucon -pisucon isuports < %s", adminDBSchemaFilePath))
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}

	db, err := adminDB()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < tenantsNum; i++ {
		log.Println("create tenant")
		tenant := createTenant()
		players := createPlayers(tenant)
		competitions := createCompetitions(tenant)
		playerScores, visitHistroies := createPlayerData(tenant, players, competitions)
		if err := storeTenant(tenant, players, competitions, playerScores); err != nil {
			log.Fatal(err)
		}
		if err := storeAdmin(db, tenant, visitHistroies); err != nil {
			log.Fatal(err)
		}
	}
}

var mu sync.Mutex
var idMap = map[int64]int64{}

func genID(ts time.Time) int64 {
	mu.Lock()
	defer mu.Unlock()
	diff := ts.Sub(epoch)
	id := int64(diff.Seconds())
	if _, exists := idMap[id]; !exists {
		idMap[id] = fake.Int64Between(0, 99)
		return id*1000 + idMap[id]
	} else if idMap[id] < 999 {
		idMap[id]++
		return id*1000 + idMap[id]
	}
	log.Fatalf("too many id at %s", ts)
	return 0
}

func adminDB() (*sqlx.DB, error) {
	config := mysql.NewConfig()
	config.Net = "tcp"
	config.Addr = "127.0.0.1:3306"
	config.User = "isucon"
	config.Passwd = "isucon"
	config.DBName = "isuports"
	config.ParseTime = true
	config.Loc = time.UTC

	return sqlx.Open("mysql", config.FormatDSN())
}

func storeAdmin(db *sqlx.DB, tenant *isuports.TenantRow, visitHistories []*isuports.VisitHistoryRow) error {
	log.Println("store admin", tenant.ID)
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err = tx.NamedExec(
		`INSERT INTO tenant (id, name, display_name, created_at, updated_at)
		 VALUES (:id, :name, :display_name, :created_at, :updated_at)`,
		tenant,
	); err != nil {
		return err
	}

	var from int
	for i, _ := range visitHistories {
		if i > 0 && i%1000 == 0 || i == len(visitHistories)-1 {
			if _, err := tx.NamedExec(
				`INSERT INTO visit_history (player_name, tenant_id, competition_id, created_at, updated_at)
				VALUES(:player_name, :tenant_id, :competition_id, :created_at, :updated_at)`,
				visitHistories[from:i],
			); err != nil {
				return err
			}
			from = i
		}
	}
	maxID := genID(now.Add(time.Second)) / 1000 * 1000
	if _, err := tx.Exec(`REPLACE INTO id_generator (id, stub) VALUES (?, ?)`, maxID, "a"); err != nil {
		return err
	}

	return tx.Commit()
	return nil
}

func storeTenant(tenant *isuports.TenantRow, players []*isuports.PlayerRow, competitions []*isuports.CompetitionRow, pss []*isuports.PlayerScoreRow) error {
	log.Println("store tenant", tenant.ID)
	os.Remove(tenant.Name + ".db")
	cmd := exec.Command("sh", "-c", fmt.Sprintf("sqlite3 %s.db < %s", tenant.Name, tenantDBSchemaFilePath))
	if err := cmd.Run(); err != nil {
		return err
	}
	db, err := sqlx.Open("sqlite3", fmt.Sprintf("file:%s.db?mode=rw", tenant.Name))
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err = tx.NamedExec(
		`INSERT INTO player (id, name, display_name, is_disqualified, created_at, updated_at)
		 VALUES (:id, :name, :display_name, :is_disqualified, :created_at, :updated_at)`,
		players,
	); err != nil {
		return err
	}
	if _, err := tx.NamedExec(
		`INSERT INTO competition (id, title, finished_at, created_at, updated_at)
		VALUES(:id, :title, :finished_at, :created_at, :updated_at)`,
		competitions,
	); err != nil {
		return err
	}
	var from int
	for i, _ := range pss {
		if i > 0 && i%1000 == 0 || i == len(pss)-1 {
			if _, err := tx.NamedExec(
				`INSERT INTO player_score (id, player_id, competition_id, score, created_at, updated_at)
				VALUES(:id, :player_id, :competition_id, :score, :created_at, :updated_at)`,
				pss[from:i],
			); err != nil {
				return err
			}
			from = i
		}
	}
	return tx.Commit()
}

func createTenant() *isuports.TenantRow {
	created := fake.Time().TimeBetween(epoch, now.Add(-time.Hour*24*1))
	id := genID(created)
	name := fmt.Sprintf("tenant-%d", id)
	tenant := isuports.TenantRow{
		ID:          id,
		Name:        name,
		DisplayName: fake.Company().Name(),
		CreatedAt:   created,
		UpdatedAt:   fake.Time().TimeBetween(created, now),
	}
	return &tenant
}

func createPlayers(tenant *isuports.TenantRow) []*isuports.PlayerRow {
	playersNum := fake.IntBetween(playersNumByTenant/10, playersNumByTenant)
	players := make([]*isuports.PlayerRow, 0, playersNum)
	for i := 0; i < playersNum; i++ {
		players = append(players, createPlayer(tenant))
	}
	sort.SliceStable(players, func(i int, j int) bool {
		return players[i].CreatedAt.Before(players[j].CreatedAt)
	})
	return players
}

func createPlayer(tenant *isuports.TenantRow) *isuports.PlayerRow {
	created := fake.Time().TimeBetween(tenant.CreatedAt, now)
	player := isuports.PlayerRow{
		ID:             genID(created),
		Name:           bench.RandomString(fake.IntBetween(8, 16)),
		DisplayName:    fake.Person().Name(),
		IsDisqualified: rand.Intn(100) < disqualifiedRate,
		CreatedAt:      created,
		UpdatedAt:      fake.Time().TimeBetween(created, now),
	}
	return &player
}

func createCompetitions(tenant *isuports.TenantRow) []*isuports.CompetitionRow {
	num := fake.IntBetween(competitionsNumByTenant/10, competitionsNumByTenant)
	rows := make([]*isuports.CompetitionRow, 0, num)
	for i := 0; i < num; i++ {
		rows = append(rows, createCompetition(tenant))
	}
	sort.SliceStable(rows, func(i int, j int) bool {
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	return rows
}

func createCompetition(tenant *isuports.TenantRow) *isuports.CompetitionRow {
	created := fake.Time().TimeBetween(tenant.CreatedAt, now)
	isFinished := rand.Intn(100) < 50
	competition := isuports.CompetitionRow{
		ID:        genID(created),
		Title:     fake.Music().Name(),
		CreatedAt: created,
	}
	if isFinished {
		competition.FinishedAt = sql.NullTime{
			Time:  fake.Time().TimeBetween(created, now),
			Valid: true,
		}
		competition.UpdatedAt = competition.FinishedAt.Time
	} else {
		competition.UpdatedAt = fake.Time().TimeBetween(created, now)
	}
	return &competition
}

func createPlayerData(
	tenant *isuports.TenantRow,
	players []*isuports.PlayerRow,
	competitions []*isuports.CompetitionRow,
) ([]*isuports.PlayerScoreRow, []*isuports.VisitHistoryRow) {
	scores := make([]*isuports.PlayerScoreRow, 0, len(players)*len(competitions))
	visits := make([]*isuports.VisitHistoryRow, 0, len(players)*len(competitions)*visitsByCompetition)
	for _, c := range competitions {
		for _, p := range players {
			if c.FinishedAt.Valid && p.CreatedAt.After(c.FinishedAt.Time) {
				// 大会が終わったあとに登録したplayerはデータがない
				continue
			}
			var end time.Time
			if c.FinishedAt.Valid {
				end = c.FinishedAt.Time
			} else {
				end = now
			}
			created := fake.Time().TimeBetween(c.CreatedAt, end)
			lastVisitedAt := fake.Time().TimeBetween(created, end)
			for i := 0; i < fake.IntBetween(visitsByCompetition/10, visitsByCompetition); i++ {
				visitedAt := fake.Time().TimeBetween(created, lastVisitedAt)
				visits = append(visits, &isuports.VisitHistoryRow{
					TenantID:      tenant.ID,
					PlayerName:    p.Name,
					CompetitionID: c.ID,
					CreatedAt:     visitedAt,
					UpdatedAt:     visitedAt,
				})
			}
			scores = append(scores, &isuports.PlayerScoreRow{
				ID:            genID(created),
				PlayerID:      p.ID,
				CompetitionID: c.ID,
				Score:         fake.Int64Between(0, 100) * fake.Int64Between(0, 100) * fake.Int64Between(0, 100),
				CreatedAt:     created,
				UpdatedAt:     created,
			})
		}
	}
	return scores, visits
}
