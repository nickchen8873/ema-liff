package routes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func SetupFitbitRoutes(r *gin.Engine, dbPool *pgxpool.Pool) { // 假設你傳入的是 pgxpool

	// 1. 登入路由 (前端 LIFF 按下按鈕後呼叫這個網址，或直接 href 導向)
	// 範例： GET /api/fitbit/login?userId=U123456...
	r.GET("/api/fitbit/login", func(c *gin.Context) {
		userId := c.Query("userId")
		if userId == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少受試者 userId"})
			return
		}

		clientID := os.Getenv("FITBIT_CLIENT_ID")
		// 在 routes/fitbit.go 中的 login 與 callback 兩個路由裡，都換成這樣寫：
		redirectURI := os.Getenv("FITBIT_REDIRECT_URI")
		if redirectURI == "" {
			// 本地測試的備援預設值
			redirectURI = "http://localhost:3000/api/fitbit/callback"
		}

		// 請求的權限範圍 (以空格分隔)
		scope := "activity sleep heartrate profile"

		// 組合授權網址 (把 userId 藏在 state 裡面)
		authURL := fmt.Sprintf(
			"https://www.fitbit.com/oauth2/authorize?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s",
			clientID, redirectURI, scope, userId,
		)

		// 直接轉址到 Fitbit 登入頁
		c.Redirect(http.StatusFound, authURL)
	})

	// 2. 回呼路由 (Fitbit 授權成功後會轉回這裡)
	r.GET("/api/fitbit/callback", func(c *gin.Context) {
		code := c.Query("code")
		state := c.Query("state") // 這裡裝的是我們剛剛塞進去的 userId

		if code == "" || state == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "授權失敗：缺少必要參數"})
			return
		}

		clientID := os.Getenv("FITBIT_CLIENT_ID")
		clientSecret := os.Getenv("FITBIT_CLIENT_SECRET")
		// 在 routes/fitbit.go 中的 login 與 callback 兩個路由裡，都換成這樣寫：
		redirectURI := os.Getenv("FITBIT_REDIRECT_URI")
		if redirectURI == "" {
			// 本地測試的備援預設值
			redirectURI = "http://localhost:3000/api/fitbit/callback"
		}

		// 準備打 API 換 Token 的資料
		data := url.Values{}
		data.Set("client_id", clientID)
		data.Set("grant_type", "authorization_code")
		data.Set("redirect_uri", redirectURI)
		data.Set("code", code)

		req, _ := http.NewRequest("POST", "https://api.fitbit.com/oauth2/token", strings.NewReader(data.Encode()))
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		// Fitbit 要求的 Basic Auth
		authStr := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
		req.Header.Add("Authorization", "Basic "+authStr)

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[錯誤] 請求 Fitbit Token 失敗: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "網路連線異常"})
			return
		}
		defer resp.Body.Close()

		// 解析回傳的 JSON
		var tokenResp struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
			UserID       string `json:"user_id"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil || tokenResp.AccessToken == "" {
			log.Printf("[錯誤] 解析 Token 失敗，可能授權碼無效")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "獲取授權失敗，請重試"})
			return
		}

		// 計算過期的絕對時間 (現在時間 + 存活秒數)
		expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

		// 使用 PostgreSQL 的 UPSERT 語法寫入 (ON CONFLICT)
		query := `
			INSERT INTO public.user_tokens (participant_id, fitbit_access_token, fitbit_refresh_token, fitbit_user_id, expires_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (participant_id) 
			DO UPDATE SET 
				fitbit_access_token = EXCLUDED.fitbit_access_token,
				fitbit_refresh_token = EXCLUDED.fitbit_refresh_token,
				fitbit_user_id = EXCLUDED.fitbit_user_id,
				expires_at = EXCLUDED.expires_at,
				updated_at = NOW();
		`

		_, err = dbPool.Exec(context.Background(), query, state, tokenResp.AccessToken, tokenResp.RefreshToken, tokenResp.UserID, expiresAt)
		if err != nil {
			log.Printf("[錯誤] 資料庫寫入 Token 失敗 userId=%s: %v", state, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "儲存授權紀錄失敗"})
			return
		}

		// 成功後，轉址回前端的 LIFF 畫面 (例如提示「綁定成功」的頁面)
		c.Redirect(http.StatusFound, "https://liff.line.me/2009889443-bwfiYEbv?status=success")
	})

	// 查詢使用者的 Fitbit 綁定狀態
	r.GET("/api/fitbit/status", func(c *gin.Context) {
		userId := c.Query("userId")
		if userId == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 userId"})
			return
		}

		// 去資料庫找找看有沒有這個人的 Token 紀錄
		var count int
		query := `SELECT COUNT(*) FROM user_tokens WHERE participant_id = $1`
		err := dbPool.QueryRow(context.Background(), query, userId).Scan(&count)

		if err != nil {
			log.Printf("[錯誤] 查詢綁定狀態失敗 userId=%s: %v", userId, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "資料庫查詢失敗"})
			return
		}

		// 如果 count 大於 0，代表有紀錄，回傳 isBound: true
		c.JSON(http.StatusOK, gin.H{"isBound": count > 0})
	})
}
