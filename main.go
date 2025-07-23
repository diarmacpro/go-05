package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
)

func main() {
	ctx := context.Background()

	// Membuat session DB
	container, err := sqlstore.New(ctx, "sqlite", "file:session.db?mode=rwc&_foreign_keys=1", waLog.Noop)
	if err != nil {
		panic(err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		panic(err)
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Noop)

	if client.Store.ID == nil {
		// Belum login → generate QR
		qrChan, _ := client.GetQRChannel(ctx)
		err := client.Connect()
		if err != nil {
			panic(err)
		}

		for evt := range qrChan {
			if evt.Event == "code" {
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				fmt.Println("📱 Scan QR dengan WhatsApp kamu.")
			} else {
				fmt.Println("QR Event:", evt.Event)
			}
		}
	} else {
		fmt.Println("✅ Sudah login sebagai:", client.Store.ID.User)
		err := client.Connect()
		if err != nil {
			panic(err)
		}
	}

	// Menunggu CTRL+C
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)
	<-c

	fmt.Println("🚪 Disconnecting...")
	client.Disconnect()
}
