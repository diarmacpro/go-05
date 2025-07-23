package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/mattn/go-sqlite3" // PENTING: registrasi driver
	"github.com/mdp/qrterminal/v3"  // tambahan agar QR tampil di terminal
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		fmt.Println("💬 Pesan masuk:", v.Message.GetConversation())
	}
}

func main() {
	dbLog := waLog.Stdout("Database", "DEBUG", true)
	ctx := context.Background()

	// pastikan driver "sqlite3" (mattn/go-sqlite3) digunakan
	container, err := sqlstore.New(ctx, "sqlite3", "file:session.db?_foreign_keys=on", dbLog)
	if err != nil {
		panic("❌ Gagal buka DB: " + err.Error())
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		panic("❌ Gagal ambil device: " + err.Error())
	}

	clientLog := waLog.Stdout("Client", "DEBUG", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)
	client.AddEventHandler(eventHandler)

	if client.Store.ID == nil {
		qrChan, _ := client.GetQRChannel(ctx)
		if err := client.Connect(); err != nil {
			panic("❌ Gagal connect: " + err.Error())
		}

		for evt := range qrChan {
			switch evt.Event {
			case "code":
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				fmt.Println("📱 Scan QR dengan WhatsApp Anda")
			case "success":
				fmt.Println("✅ Login berhasil!")
			case "timeout":
				fmt.Println("⏱️  QR timeout")
			default:
				fmt.Println("ℹ️  QR event:", evt.Event)
			}
		}
	} else {
		fmt.Println("✅ Sudah login sebagai", client.Store.ID.User)
		if err := client.Connect(); err != nil {
			panic("❌ Gagal reconnect: " + err.Error())
		}
	}

	// tunggu CTRL+C
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	fmt.Println("🚪 Disconnecting...")
	client.Disconnect()
}
