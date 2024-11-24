package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/v4"
)

type ReactionModel struct {
	ID           int64  `db:"id"`
	EmojiName    string `db:"emoji_name"`
	UserID       int64  `db:"user_id"`
	LivestreamID int64  `db:"livestream_id"`
	CreatedAt    int64  `db:"created_at"`
}

type Reaction struct {
	ID         int64      `json:"id"`
	EmojiName  string     `json:"emoji_name"`
	User       User       `json:"user"`
	Livestream Livestream `json:"livestream"`
	CreatedAt  int64      `json:"created_at"`
}

type PostReactionRequest struct {
	EmojiName string `json:"emoji_name"`
}

func getReactionsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		// echo.NewHTTPErrorが返っているのでそのまま出力
		return err
	}

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	type ReactionWithDetails struct {
		ID                         int64  `db:"id"`
		EmojiName                  string `db:"emoji_name"`
		CreatedAt                  int64  `db:"created_at"`
		UserID                     int64  `db:"user_id"`
		UserName                   string `db:"user_name"`
		UserDisplayName            string `db:"user_display_name"`
		UserDescription            string `db:"user_description"`
		UserThemeID                int64  `db:"user_theme_id"`
		UserDarkMode               bool   `db:"user_dark_mode"`
		UserIconImage              []byte `db:"user_icon_image"`
		LivestreamID               int64  `db:"livestream_id"`
		LivestreamOwnerID          int64  `db:"livestream_owner_id"`
		LivestreamOwnerName        string `db:"livestream_owner_name"`
		LivestreamOwnerDisplayName string `db:"livestream_owner_display_name"`
		LivestreamOwnerDescription string `db:"livestream_owner_description"`
		LivestreamOwnerThemeID     int64  `db:"livestream_owner_theme_id"`
		LivestreamOwnerDarkMode    bool   `db:"livestream_owner_dark_mode"`
		LivestreamOwnerIconImage   []byte `db:"livestream_owner_icon_image"`
		LivestreamTitle            string `db:"livestream_title"`
		LivestreamDescription      string `db:"livestream_description"`
		LivestreamPlaylistURL      string `db:"livestream_playlist_url"`
		LivestreamThumbnailURL     string `db:"livestream_thumbnail_url"`
		LivestreamStartAt          int64  `db:"livestream_start_at"`
		LivestreamEndAt            int64  `db:"livestream_end_at"`
	}

	reactions := []ReactionWithDetails{}
	query := `
    SELECT 
        r.id,
        r.emoji_name,
        r.created_at,
        u.id AS user_id,
        u.name AS user_name,
        u.display_name AS user_display_name,
        u.description AS user_description,
        ut.id AS user_theme_id,
        ut.dark_mode AS user_dark_mode,
        ui.image AS user_icon_image,
        ls.id AS livestream_id,
        ls.title AS livestream_title,
        ls.description AS livestream_description,
        ls.playlist_url AS livestream_playlist_url,
        ls.thumbnail_url AS livestream_thumbnail_url,
        ls.start_at AS livestream_start_at,
        ls.end_at AS livestream_end_at,
		o.id AS livestream_owner_id,
        o.name AS livestream_owner_name,
        o.display_name AS livestream_owner_display_name,
        o.description AS livestream_owner_description,
        ot.id AS livestream_owner_theme_id,
        ot.dark_mode AS livestream_owner_dark_mode,
        oi.image AS livestream_owner_icon_image
    FROM 
        reactions r
    INNER JOIN 
        users u ON r.user_id = u.id
	LEFT JOIN
		themes ut ON u.id = ut.user_id
	LEFT JOIN
		icons ui ON u.id = ui.user_id
    INNER JOIN 
        livestreams ls ON r.livestream_id = ls.id
    INNER JOIN
		users o ON ls.user_id = o.id
	LEFT JOIN
		themes ot ON o.id = ot.user_id
	LEFT JOIN
		icons oi ON o.id = oi.user_id
    WHERE 
        r.livestream_id = ?
    ORDER BY 
        r.created_at DESC
`

	if c.QueryParam("limit") != "" {
		limit, err := strconv.Atoi(c.QueryParam("limit"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "limit query parameter must be integer")
		}
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	err = tx.SelectContext(ctx, &reactions, query, livestreamID)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusOK, []*ReactionWithDetails{})
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get reactions: "+err.Error())
	}

	var tags []Tag
	query = "SELECT tags.* FROM tags JOIN livestream_tags ON tags.id = livestream_tags.tag_id WHERE livestream_tags.livestream_id = ?"
	err = tx.SelectContext(ctx, &tags, query, livestreamID)
	if !errors.Is(err, sql.ErrNoRows) && err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomments: "+err.Error())
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	reactionsResponse := []Reaction{}
	image, err := os.ReadFile(fallbackImage)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed read fallback image: "+err.Error())
	}
	fallbackImageHash := fmt.Sprintf("%x", sha256.Sum256(image))
	for i := range reactions {
		userIconHash := fallbackImageHash
		if reactions[i].UserIconImage != nil {
			userIconHash = fmt.Sprintf("%x", sha256.Sum256(reactions[i].UserIconImage))
		}
		livestreamOwnerIconHash := fallbackImageHash
		if reactions[i].LivestreamOwnerIconImage != nil {
			livestreamOwnerIconHash = fmt.Sprintf("%x", sha256.Sum256(reactions[i].LivestreamOwnerIconImage))
		}

		reactionsResponse[i] = Reaction{
			ID:        reactions[i].ID,
			EmojiName: reactions[i].EmojiName,
			CreatedAt: reactions[i].CreatedAt,
			User: User{
				ID:          reactions[i].UserID,
				Name:        reactions[i].UserName,
				DisplayName: reactions[i].UserDisplayName,
				Description: reactions[i].UserDescription,
				Theme: Theme{
					ID:       reactions[i].UserThemeID,
					DarkMode: reactions[i].UserDarkMode,
				},
				IconHash: userIconHash,
			},
			Livestream: Livestream{
				ID: reactions[i].LivestreamID,
				Owner: User{
					ID:          reactions[i].LivestreamOwnerID,
					Name:        reactions[i].LivestreamOwnerName,
					DisplayName: reactions[i].LivestreamOwnerDisplayName,
					Description: reactions[i].LivestreamOwnerDescription,
					Theme: Theme{
						ID:       reactions[i].LivestreamOwnerThemeID,
						DarkMode: reactions[i].LivestreamOwnerDarkMode,
					},
					IconHash: livestreamOwnerIconHash,
				},
				Title:        reactions[i].LivestreamTitle,
				Description:  reactions[i].LivestreamDescription,
				PlaylistUrl:  reactions[i].LivestreamPlaylistURL,
				ThumbnailUrl: reactions[i].LivestreamThumbnailURL,
				StartAt:      reactions[i].LivestreamStartAt,
				EndAt:        reactions[i].LivestreamEndAt,
				Tags:         tags,
			},
		}
	}

	return c.JSON(http.StatusOK, reactionsResponse)
}

func postReactionHandler(c echo.Context) error {
	ctx := c.Request().Context()
	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	if err := verifyUserSession(c); err != nil {
		// echo.NewHTTPErrorが返っているのでそのまま出力
		return err
	}

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

	var req *PostReactionRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "failed to decode the request body as json")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	reactionModel := ReactionModel{
		UserID:       int64(userID),
		LivestreamID: int64(livestreamID),
		EmojiName:    req.EmojiName,
		CreatedAt:    time.Now().Unix(),
	}

	result, err := tx.NamedExecContext(ctx, "INSERT INTO reactions (user_id, livestream_id, emoji_name, created_at) VALUES (:user_id, :livestream_id, :emoji_name, :created_at)", reactionModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert reaction: "+err.Error())
	}

	reactionID, err := result.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted reaction id: "+err.Error())
	}
	reactionModel.ID = reactionID

	reaction, err := fillReactionResponse(ctx, tx, reactionModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fill reaction: "+err.Error())
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusCreated, reaction)
}

func fillReactionResponse(ctx context.Context, tx *sqlx.Tx, reactionModel ReactionModel) (Reaction, error) {
	userModel := UserModel{}
	if err := tx.GetContext(ctx, &userModel, "SELECT * FROM users WHERE id = ?", reactionModel.UserID); err != nil {
		return Reaction{}, err
	}
	user, err := fillUserResponse(ctx, tx, userModel)
	if err != nil {
		return Reaction{}, err
	}

	livestreamModel := LivestreamModel{}
	if err := tx.GetContext(ctx, &livestreamModel, "SELECT * FROM livestreams WHERE id = ?", reactionModel.LivestreamID); err != nil {
		return Reaction{}, err
	}
	livestream, err := fillLivestreamResponse(ctx, tx, livestreamModel)
	if err != nil {
		return Reaction{}, err
	}

	reaction := Reaction{
		ID:         reactionModel.ID,
		EmojiName:  reactionModel.EmojiName,
		User:       user,
		Livestream: livestream,
		CreatedAt:  reactionModel.CreatedAt,
	}

	return reaction, nil
}
