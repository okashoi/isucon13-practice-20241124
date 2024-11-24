package main

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/labstack/echo/v4"
)

type LivestreamStatistics struct {
	Rank           int64 `json:"rank"`
	ViewersCount   int64 `json:"viewers_count"`
	TotalReactions int64 `json:"total_reactions"`
	TotalReports   int64 `json:"total_reports"`
	MaxTip         int64 `json:"max_tip"`
}

type LivestreamRankingEntry struct {
	LivestreamID int64
	Score        int64
}
type LivestreamRanking []LivestreamRankingEntry

func (r LivestreamRanking) Len() int      { return len(r) }
func (r LivestreamRanking) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r LivestreamRanking) Less(i, j int) bool {
	if r[i].Score == r[j].Score {
		return r[i].LivestreamID < r[j].LivestreamID
	} else {
		return r[i].Score < r[j].Score
	}
}

type UserStatistics struct {
	Rank              int64  `json:"rank"`
	ViewersCount      int64  `json:"viewers_count"`
	TotalReactions    int64  `json:"total_reactions"`
	TotalLivecomments int64  `json:"total_livecomments"`
	TotalTip          int64  `json:"total_tip"`
	FavoriteEmoji     string `json:"favorite_emoji"`
}

type UserRankingEntry struct {
	Username string
	Score    int64
}
type UserRanking []UserRankingEntry

func (r UserRanking) Len() int      { return len(r) }
func (r UserRanking) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r UserRanking) Less(i, j int) bool {
	if r[i].Score == r[j].Score {
		return r[i].Username < r[j].Username
	} else {
		return r[i].Score < r[j].Score
	}
}

func getUserStatisticsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		// echo.NewHTTPErrorが返っているのでそのまま出力
		return err
	}

	username := c.Param("username")
	// ユーザごとに、紐づく配信について、累計リアクション数、累計ライブコメント数、累計売上金額を算出
	// また、現在の合計視聴者数もだす

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var user UserModel
	if err := tx.GetContext(ctx, &user, "SELECT * FROM users WHERE name = ?", username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusBadRequest, "not found user that has the given username")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get user: "+err.Error())
		}
	}
	var userTotalReactions int64
	var userTotalTip int64

	// ランク算出
	var users []*UserModel
	if err := tx.SelectContext(ctx, &users, "SELECT * FROM users"); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get users: "+err.Error())
	}

	userScore := map[int64]int64{}

	type ReactionCount struct {
		UserID        int64 `db:"user_id"`
		ReactionCount int64 `db:"reaction_count"`
	}
	query := `
		SELECT
		    u.id AS user_id,
		    COUNT(r.id) AS reaction_count
		FROM
		    users u
		INNER JOIN livestreams l ON l.user_id = u.id
		INNER JOIN reactions r ON r.livestream_id = l.id
		GROUP BY u.id
`
	reactionCounts := []ReactionCount{}
	if err := tx.SelectContext(ctx, &reactionCounts, query); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count reactions: "+err.Error())
	}
	for _, rc := range reactionCounts {
		userScore[rc.UserID] = rc.ReactionCount
		if rc.UserID == user.ID {
			userTotalReactions = rc.ReactionCount
		}
	}

	type TotalTip struct {
		UserID   int64 `db:"user_id"`
		TotalTip int64 `db:"total_tip"`
	}
	query = `
		SELECT
		    u.id AS user_id,
		    IFNULL(SUM(lc.tip), 0) AS total_tip
		FROM
		    users u
		INNER JOIN livestreams ls ON ls.user_id = u.id
		INNER JOIN livecomments lc ON lc.livestream_id = ls.id
		GROUP BY u.id
`
	totalTips := []TotalTip{}
	if err := tx.SelectContext(ctx, &totalTips, query); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count tips: "+err.Error())
	}
	for _, tt := range totalTips {
		userScore[tt.UserID] += tt.TotalTip
		if tt.UserID == user.ID {
			userTotalTip = tt.TotalTip
		}
	}

	ranking := make(UserRanking, len(users))
	for _, user := range users {
		score := userScore[user.ID]
		ranking = append(ranking, UserRankingEntry{
			Username: user.Name,
			Score:    score,
		})
	}
	sort.Sort(ranking)

	var rank int64 = 1
	for i := len(ranking) - 1; i >= 0; i-- {
		entry := ranking[i]
		if entry.Username == username {
			break
		}
		rank++
	}

	// ライブコメント数、合計視聴者数
	var totalLivecomments int64
	var viewersCount int64
	if err := tx.GetContext(ctx, &totalLivecomments, "SELECT COUNT(lc.id) FROM livecomments lc INNER JOIN livestreams ls ON lc.livestream_id = ls.id WHERE ls.user_id = ?", user.ID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomments count: "+err.Error())
	}
	if err := tx.GetContext(ctx, &viewersCount, "SELECT COUNT(lvh.id) FROM livestream_viewers_history lvh INNER JOIN livestreams ls ON lvh.livestream_id = ls.id WHERE ls.user_id = ?", user.ID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get viewers count: "+err.Error())
	}

	// お気に入り絵文字
	var favoriteEmoji string
	query = `
	SELECT r.emoji_name
	FROM users u
	INNER JOIN livestreams l ON l.user_id = u.id
	INNER JOIN reactions r ON r.livestream_id = l.id
	WHERE u.name = ?
	GROUP BY emoji_name
	ORDER BY COUNT(*) DESC, emoji_name DESC
	LIMIT 1
	`

	if err := tx.GetContext(ctx, &favoriteEmoji, query, username); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to find favorite emoji: "+err.Error())
	}

	stats := UserStatistics{
		Rank:              rank,
		ViewersCount:      viewersCount,
		TotalReactions:    userTotalReactions,
		TotalLivecomments: totalLivecomments,
		TotalTip:          userTotalTip,
		FavoriteEmoji:     favoriteEmoji,
	}
	return c.JSON(http.StatusOK, stats)
}

func getLivestreamStatisticsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	id, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}
	livestreamID := int64(id)

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	_, err = getLivestream(ctx, tx, int(livestreamID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusBadRequest, "cannot get stats of not found livestream")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestream: "+err.Error())
		}
	}
	var totalReactions int64

	var livestreams []*LivestreamModel
	if err := tx.SelectContext(ctx, &livestreams, "SELECT * FROM livestreams"); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestreams: "+err.Error())
	}

	// ランク算出
	livestreamScore := map[int64]int64{}
	type ReactionCount struct {
		LivestreamID  int64 `db:"livestream_id"`
		ReactionCount int64 `db:"reaction_count"`
	}
	query := `
	SELECT
	    l.id AS livestream_id,
		COUNT(r.id) AS reaction_count
	FROM
		livestreams l
	INNER JOIN reactions r ON l.id = r.livestream_id
	GROUP BY l.id
`
	reactionCounts := []ReactionCount{}
	if err := tx.SelectContext(ctx, &reactionCounts, query); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count reactions: "+err.Error())
	}
	for _, rc := range reactionCounts {
		livestreamScore[rc.LivestreamID] = rc.ReactionCount
		if rc.LivestreamID == livestreamID {
			totalReactions = rc.ReactionCount
		}
	}

	type TotalTip struct {
		LivestreamID int64 `db:"livestream_id"`
		TotalTip     int64 `db:"total_tip"`
	}
	query = `
	SELECT
	    l.id AS livestream_id,
		IFNULL(SUM(l2.tip), 0) AS total_tip
	FROM
	    livestreams l
	INNER JOIN livecomments l2 ON l.id = l2.livestream_id
	GROUP BY l.id
`
	totalTips := []TotalTip{}
	if err := tx.SelectContext(ctx, &totalTips, query); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count tips: "+err.Error())
	}
	for _, tt := range totalTips {
		livestreamScore[tt.LivestreamID] += tt.TotalTip
	}

	ranking := make(LivestreamRanking, len(livestreams))
	for _, livestream := range livestreams {
		score := livestreamScore[livestream.ID]
		ranking = append(ranking, LivestreamRankingEntry{
			LivestreamID: livestream.ID,
			Score:        score,
		})
	}
	sort.Sort(ranking)

	var rank int64 = 1
	for i := len(ranking) - 1; i >= 0; i-- {
		entry := ranking[i]
		if entry.LivestreamID == livestreamID {
			break
		}
		rank++
	}

	// 視聴者数算出
	var viewersCount int64
	if err := tx.GetContext(ctx, &viewersCount, `SELECT COUNT(h.id) FROM livestreams l INNER JOIN livestream_viewers_history h ON h.livestream_id = l.id WHERE l.id = ?`, livestreamID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count livestream viewers: "+err.Error())
	}

	// 最大チップ額
	var maxTip int64
	if err := tx.GetContext(ctx, &maxTip, `SELECT IFNULL(MAX(tip), 0) FROM livestreams l INNER JOIN livecomments l2 ON l2.livestream_id = l.id WHERE l.id = ?`, livestreamID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to find maximum tip livecomment: "+err.Error())
	}

	// スパム報告数
	var totalReports int64
	if err := tx.GetContext(ctx, &totalReports, `SELECT COUNT(r.id) FROM livestreams l INNER JOIN livecomment_reports r ON r.livestream_id = l.id WHERE l.id = ?`, livestreamID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count total spam reports: "+err.Error())
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusOK, LivestreamStatistics{
		Rank:           rank,
		ViewersCount:   viewersCount,
		MaxTip:         maxTip,
		TotalReactions: totalReactions,
		TotalReports:   totalReports,
	})
}
