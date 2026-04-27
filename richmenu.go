package main

import (
	"log"

	"github.com/line/line-bot-sdk-go/v7/linebot"
)

// SetupRichMenu 負責建立並設定全域預設選單
func SetupRichMenu(bot *linebot.Client) {
	// 🚨 【手動填寫】請確保此處為正確的 LIFF URL
	liffURL := "https://liff.line.me/2009889443-bwfiYEbv"

	// 1. 定義 Rich Menu 結構
	richMenu := linebot.RichMenu{
		Size:        linebot.RichMenuSize{Width: 2500, Height: 1686},
		Selected:    true,
		Name:        "EMA_Default_Menu",
		ChatBarText: "點我開啟紀錄",
		Areas: []linebot.AreaDetail{
			{
				Bounds: linebot.RichMenuBounds{X: 0, Y: 0, Width: 2500, Height: 1686},
				// 加上 & 符號，將其轉為指標型別以符合介面要求
				Action: linebot.RichMenuAction{
					Type:  linebot.RichMenuActionTypeURI,
					Label: "開啟紀錄面板",
					URI:   liffURL,
				},
			},
		},
	}

	// 2. 建立 Rich Menu 並取得 ID
	res, err := bot.CreateRichMenu(richMenu).Do()
	if err != nil {
		log.Fatal("建立 Rich Menu 失敗:", err)
	}
	richMenuID := res.RichMenuID
	log.Printf("[系統] 已建立 Rich Menu: %s\n", richMenuID)

	// 3. 上傳圖片
	// 🚨 【手動確認】確保專案目錄下有一張名為 rich_menu.jpg 的圖片
	if _, err := bot.UploadRichMenuImage(richMenuID, "rich_menu.jpg").Do(); err != nil {
		log.Fatal("上傳 Rich Menu 圖片失敗:", err)
	}
	log.Println("[系統] Rich Menu 圖片上傳成功")

	// 4. 設定為全域預設選單
	if _, err := bot.SetDefaultRichMenu(richMenuID).Do(); err != nil {
		log.Fatal("設定預設 Rich Menu 失敗:", err)
	}
	log.Println("[系統] 已成功將 Rich Menu 設定為預設選單")
}
