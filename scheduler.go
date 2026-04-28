package main

import (
	"context" // 【新增】
	"log"
	"time"

	// 【新增】
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/line/line-bot-sdk-go/v7/linebot"
	"github.com/robfig/cron/v3"
)

// InitScheduler 接收連線池指針
func InitScheduler(bot *linebot.Client, db *pgxpool.Pool) {
	loc, _ := time.LoadLocation("Asia/Taipei")
	c := cron.New(cron.WithLocation(loc))

	// 1. 固定排程 (範例：09:00)
	c.AddFunc("46 19 * * *", func() {
		broadcastMulticast(bot, db, "現在狀態如何？來記錄一下日常脈絡吧。")
	})

	// 2. 每天凌晨重新計算隨機排程
	c.AddFunc("1 0 * * *", func() {
		scheduleRandomPushes(bot, db, loc)
	})

	// 啟動時跑一次隨機排程計算
	scheduleRandomPushes(bot, db, loc)

	c.Start()
	log.Println("[系統] 全體推播排程器已啟動")
}

// broadcastPush 負責撈取所有使用者並發送訊息
func broadcastMulticast(bot *linebot.Client, db *pgxpool.Pool, text string) {
	// 1. 撈取所有受試者 ID
	query := `
        SELECT DISTINCT line_user_id 
        FROM ema_logs 
        WHERE line_user_id LIKE 'U%' 
          AND LENGTH(line_user_id) = 33
    `
	rows, err := db.Query(context.Background(), query)
	if err != nil {
		log.Println("[錯誤] 撈取名單失敗:", err)
		return
	}
	defer rows.Close()

	var userIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			userIDs = append(userIDs, id)
		}
	}

	if len(userIDs) == 0 {
		return
	}

	// 2. 準備訊息模板
	// 🚨 【手動填寫】請確保此處為正確的 LIFF URL
	liffURL := "https://liff.line.me/2009889443-bwfiYEbv"
	template := linebot.NewButtonsTemplate(
		"", "日常脈絡紀錄", text,
		linebot.NewURIAction("開啟紀錄面板", liffURL),
	)
	message := linebot.NewTemplateMessage("請填寫當下狀態", template)

	// 3. 執行 Multicast (分批處理，每批最多 500 人)
	batchSize := 500
	for i := 0; i < len(userIDs); i += batchSize {
		end := i + batchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}

		targetBatch := userIDs[i:end]

		// 使用 Multicast API
		if _, err := bot.Multicast(targetBatch, message).Do(); err != nil {
			log.Printf("[錯誤] Multicast 批次發送失敗: %v\n", err)
		} else {
			log.Printf("[成功] 已批次推播給 %d 位使用者\n", len(targetBatch))
		}
	}
}

// scheduleRandomPushes 也需要更新為呼叫 broadcastPush
func scheduleRandomPushes(bot *linebot.Client, db *pgxpool.Pool, loc *time.Location) {
	// ... (之前的隨機邏輯不變，只需把原本的 sendPushMessage 改為呼叫 broadcastPush)
	// 範例：
	// time.AfterFunc(time.Until(pushTime1), func() {
	//     broadcastPush(bot, db, "現在狀態如何？來記錄一下日常脈絡吧。")
	// })
}
