package main

import (
	"context" // 【新增】
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/line/line-bot-sdk-go/v7/linebot"
	// 【新增】PostgreSQL 驅動
)

// === 設定區 (請填入你的金鑰) ===
const (
	LineChannelSecret = "b17a2f8b1a342c07d39118b76944ecc8"
	LineAccessToken   = "T6xfR2zLXteZz4sEQullwc1Ut6rYKjrDfRClZkt/wzLr/9CsUa7xG3bVA17euvhrlnwL4n5BR5GktZHt8uSYXlTUZVlj6UH6uBOWjfqfpR6Cc0haRbvZYYv3lANhUqwOq1tAOcZNDxwHqOroN37FOgdB04t89/1O/w1cDnyilFU="
	MoenvAPIKey       = "5e440e4a-bfe0-45df-9c5a-192261e02520"
	TargetStation     = "古亭"
	DatabaseURL       = "postgresql://postgres.ouqodwifvpjsncdvjilg:Amitabha-4818@aws-1-ap-northeast-1.pooler.supabase.com:6543/postgres"
)

// 單一測站的資料內容
type AQIRecord struct {
	Sitename  string `json:"sitename"`
	PM25      string `json:"pm2.5"`
	Longitude string `json:"longitude"`
	Latitude  string `json:"latitude"`
}

// 完整的 API 回傳包裹
type AQIResponse struct {
	Records []AQIRecord `json:"records"`
}

type EMAPayload struct {
	MoodScore   int     `json:"moodScore"`
	EnergyScore int     `json:"energyScore"`
	Context     string  `json:"context"`
	UserId      string  `json:"userId"`
	Lat         float64 `json:"lat"` // 必須有這行
	Lng         float64 `json:"lng"` // 必須有這行
}

type HistoryRecord struct {
	Date        string `json:"date"`        // 格式化後的時間 (例如 04/28)
	MoodScore   int    `json:"moodScore"`   // 效價
	EnergyScore int    `json:"energyScore"` // 喚醒度
	PM25        int    `json:"pm25"`        // 環境數據
}

// ... (getPM25 函式維持不變) ...
func getPM25(station string) (int, error) {
	moenvKey := os.Getenv("MOENV_API_KEY")

	url := fmt.Sprintf("https://data.moenv.gov.tw/api/v2/aqx_p_432?api_key=%s&limit=1000&format=json", moenvKey)
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("API 錯誤狀態碼: %d", resp.StatusCode)
	}

	var records []AQIRecord // ✅ 關鍵修正：直接宣告為陣列
	if err := json.Unmarshal(bodyBytes, &records); err != nil {
		return 0, err
	}

	for _, r := range records { // ✅ 迴圈直接跑 records 陣列
		if r.Sitename == station {
			if r.PM25 == "" {
				return -1, fmt.Errorf("測站 %s 暫無資料", station)
			}
			pm25Int, _ := strconv.Atoi(r.PM25)
			return pm25Int, nil
		}
	}
	return 0, fmt.Errorf("找不到測站: %s", station)
}

// haversine 計算距離
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	lat1Rad, lon1Rad := lat1*math.Pi/180.0, lon1*math.Pi/180.0
	lat2Rad, lon2Rad := lat2*math.Pi/180.0, lon2*math.Pi/180.0
	dLat, dLon := lat2Rad-lat1Rad, lon2Rad-lon1Rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// getNearestPM25 取得最近測站名稱與數值
func getNearestPM25(lat, lng float64) (string, int, error) {
	apiURL := "https://data.moenv.gov.tw/api/v2/aqx_p_432?api_key=" + MoenvAPIKey + "&limit=1000&sort=ImportDate%20desc&format=JSON"

	resp, err := http.Get(apiURL)
	if err != nil {
		return "", -1, err
	}
	defer resp.Body.Close()

	var records []AQIRecord // ✅ 關鍵修正：直接宣告為陣列
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return "", -1, err
	}

	nearestStation := "未知"
	minDistance := math.MaxFloat64
	pm25Value := -1

	for _, record := range records { // ✅ 迴圈直接跑 records 陣列
		stationLat, _ := strconv.ParseFloat(record.Latitude, 64)
		stationLng, _ := strconv.ParseFloat(record.Longitude, 64)

		dist := haversineDistance(lat, lng, stationLat, stationLng)
		if dist < minDistance && record.PM25 != "" {
			minDistance = dist
			nearestStation = record.Sitename
			pm25Value, _ = strconv.Atoi(record.PM25)
		}
	}
	return nearestStation, pm25Value, nil
}

func main() {
	// 嘗試讀取 .env 檔案（在本機開發時有用，上雲端時找不到也不會報錯）
	_ = godotenv.Load()

	// 從環境變數中取得機密資料
	lineSecret := os.Getenv("LINE_CHANNEL_SECRET")
	lineToken := os.Getenv("LINE_ACCESS_TOKEN")
	dbURL := os.Getenv("DATABASE_URL")

	// 1. 初始化 LINE Bot 客戶端
	bot, err := linebot.New(lineSecret, lineToken)

	// SetupRichMenu(bot)

	if err != nil {
		log.Fatal("LINE Bot 初始化失敗:", err)
	}

	// 3. ✅ 替換為結構化的 Config 寫法：
	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Fatal("解析資料庫設定失敗:", err)
	}

	// 關鍵修正：強制 pgx 放棄預處理語句，改用不依賴快取的簡單協議
	// 這能完美相容 Supabase 的 Transaction Mode 連線池
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	// 使用設定好的 config 建立連線池
	dbPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		log.Fatal("無法建立資料庫連線池:", err)
	}
	defer dbPool.Close()

	// 啟動排程器，傳入 dbPool
	InitScheduler(bot, dbPool)

	// 建立 Gin 路由
	r := gin.Default()
	r.Static("/public", "./public")

	// 👇 【新增這段 CORS 設定】 👇
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true                                                              // 允許所有網域跨站請求 (MVP 階段先全開)
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} // 確保有 OPTIONS
	corsConfig.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization", "Accept"}

	// 掛載中介軟體
	r.Use(cors.New(corsConfig))

	// LINE Webhook 路由
	r.POST("/webhook", func(c *gin.Context) {
		events, err := bot.ParseRequest(c.Request)
		if err != nil {
			c.Writer.WriteHeader(http.StatusBadRequest)
			return
		}

		for _, event := range events {
			if event.Type == linebot.EventTypeFollow {
				// 當使用者加入好友時觸發
				welcomeMsg := "歡迎來到日常脈絡紀錄。我們會不定期關心您的狀態，您也能隨時點擊選單抒發心情。請點擊下方按鈕，開始我們的第一次紀錄。"

				template := linebot.NewButtonsTemplate(
					"", "歡迎加入", welcomeMsg,
					linebot.NewURIAction("進行首次紀錄", "https://liff.line.me/2009889443-bwfiYEbv"),
				)

				if _, err := bot.ReplyMessage(event.ReplyToken, linebot.NewTemplateMessage("歡迎訊息", template)).Do(); err != nil {
					log.Println("發送歡迎訊息失敗:", err)
				}
			}
		}

		for _, event := range events {
			// 【加上這一行】每次收到訊息，就把發送者的 User ID 印在終端機
			log.Println("👉 抓到了！這個使用者的 User ID 是:", event.Source.UserID)

			if event.Type == linebot.EventTypeMessage {
				switch message := event.Message.(type) {
				case *linebot.TextMessage:
					if message.Text == "紀錄" {
						template := linebot.NewButtonsTemplate(
							"", "請填寫當下狀態", "現在感覺如何？",
							linebot.NewURIAction("開啟紀錄面板", "https://liff.line.me/2009889443-bwfiYEbv"),
						)
						if _, err = bot.ReplyMessage(event.ReplyToken, linebot.NewTemplateMessage("請填寫當下狀態", template)).Do(); err != nil {
							log.Println("回覆訊息失敗:", err)
						}
					}
				}
			}
		}
	})

	// 接收 LIFF 傳來的問卷資料
	r.POST("/api/submit-ema", func(c *gin.Context) {
		var payload EMAPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "無效的資料格式"})
			return
		}

		// --- 【修改區塊開始】 ---
		var currentPM25 int
		var currentStation string = "古亭" // 預設測站

		// 判斷前端是否有傳送有效的座標
		if payload.Lat != 0 && payload.Lng != 0 {
			station, pm25, err := getNearestPM25(payload.Lat, payload.Lng)
			if err != nil {
				log.Println("[警告] 抓取最近測站失敗，降級使用預設測站:", err)
				currentPM25, _ = getPM25(currentStation) // 備援：用原本的舊方法
			} else {
				currentStation = station
				currentPM25 = pm25
				log.Printf("[系統] 定位成功！經緯度(%.4f, %.4f) 最近測站為: %s", payload.Lat, payload.Lng, currentStation)
			}
		} else {
			// 使用者拒絕授權定位，直接使用預設測站
			log.Println("[系統] 無 GPS 座標，使用預設測站:", currentStation)
			currentPM25, err = getPM25(currentStation)
			if err != nil {
				currentPM25 = -1
			}
		}
		// --- 【修改區塊結束】 ---

		// 在 INSERT 之前檢查
		if len(payload.UserId) != 33 || payload.UserId[0] != 'U' {
			log.Printf("[警告] 收到無效的 UserID: %s，將不寫入資料庫", payload.UserId)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
			return
		}
		// 【新增】將資料寫入 Supabase (PostgreSQL)
		query := `
			INSERT INTO ema_logs (line_user_id, mood_score, energy_score, context, pm25_value, station_name)
			VALUES ($1, $2, $3, $4, $5, $6)
		`
		// 把 payload.UserId 和 payload.EnergyScore 塞進去
		_, err = dbPool.Exec(context.Background(), query, payload.UserId, payload.MoodScore, payload.EnergyScore, payload.Context, currentPM25, currentStation)

		if err != nil {
			log.Printf("寫入資料庫失敗: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "儲存失敗"})
			return
		}

		log.Println("==== 成功寫入一筆 EMA 紀錄至資料庫 ====")
		log.Printf("心情分數: %d | 情境: %s | PM2.5: %d", payload.MoodScore, payload.Context, currentStation, currentPM25)
		log.Println("======================================")

		c.JSON(http.StatusOK, gin.H{"success": true, "message": "紀錄已成功儲存！"})
	})

	// 獲取個人歷史紀錄
	r.GET("/api/history", func(c *gin.Context) {
		userId := c.Query("userId")
		if len(userId) != 33 || userId[0] != 'U' {
			c.JSON(http.StatusBadRequest, gin.H{"error": "無效的 UserID"})
			return
		}

		// 撈取該受試者最近的 14 筆紀錄，依照時間舊到新排序 (適合畫折線圖)
		query := `
			SELECT created_at, mood_score, energy_score, pm25_value 
			FROM ema_logs 
			WHERE line_user_id = $1 
			ORDER BY created_at ASC 
			LIMIT 14
		`
		rows, err := dbPool.Query(context.Background(), query, userId)
		if err != nil {
			log.Printf("[錯誤] 撈取歷史紀錄失敗: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "讀取失敗"})
			return
		}
		defer rows.Close()

		var records []HistoryRecord
		for rows.Next() {
			var r HistoryRecord
			var createdAt time.Time
			if err := rows.Scan(&createdAt, &r.MoodScore, &r.EnergyScore, &r.PM25); err == nil {
				// 將時間轉為台北時區，並格式化為 "MM/DD HH:mm"
				loc, _ := time.LoadLocation("Asia/Taipei")
				r.Date = createdAt.In(loc).Format("01/02 15:04")
				records = append(records, r)
			}
		}

		c.JSON(http.StatusOK, records)
	})

	log.Println("[系統] Go 伺服器已啟動在 http://localhost:3000")
	r.Run(":3000")
}
