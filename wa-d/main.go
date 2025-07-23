package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

var (
	cli        *whatsmeow.Client
	webhookURL string
	whMutex    sync.Mutex
	startTime  = time.Now()
)

func main() {
	dbLog := waLog.Noop
	ctx := context.Background()
	container, _ := sqlstore.New(ctx, "sqlite3", "file:session.db?_foreign_keys=on", dbLog)
	deviceStore, _ := container.GetFirstDevice(ctx)
	cli = whatsmeow.NewClient(deviceStore, dbLog)
	cli.AddEventHandler(eventHandler)

	go connectWhatsApp()

	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/send", sendHandler)
	http.HandleFunc("/webhook", webhookHandler)

	go http.ListenAndServe(":8080", nil)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	cli.Disconnect()
}

func connectWhatsApp() {
	if cli.Store.ID == nil {
		qrChan, _ := cli.GetQRChannel(context.Background())
		_ = cli.Connect()
		for evt := range qrChan {
			if evt.Event == "code" {
				qrCode, _ := qr.Encode(evt.Code, qr.M, qr.Auto)
				qrCode, _ = barcode.Scale(qrCode, 256, 256)
				f, _ := os.Create("qr.png")
				_ = png.Encode(f, qrCode)
				f.Close()
			}
		}
	} else {
		_ = cli.Connect()
	}
}

func eventHandler(raw interface{}) {
	switch v := raw.(type) {
	case *events.Message:
		if v.Info.IsFromMe {
			return
		}
		text := v.Message.GetConversation()
		if text == "" { // skip kalau kosong / null
			return
		}
		payload := map[string]interface{}{
			"from":    v.Info.Sender.String(),
			"message": text,
			"time":    v.Info.Timestamp.Unix(),
		}
		go pushWebhook(payload)
	}
}

func pushWebhook(payload interface{}) {
	whMutex.Lock()
	defer whMutex.Unlock()
	if webhookURL == "" {
		return
	}
	body, _ := json.Marshal(payload)
	http.Post(webhookURL, "application/json", bytes.NewReader(body))
}

type loginResp struct {
	Status      string    `json:"status"`
	QRFile      string    `json:"qr_file,omitempty"`
	LoggedInAs  string    `json:"logged_in_as,omitempty"`
	LoginTime   time.Time `json:"login_time,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := loginResp{GeneratedAt: time.Now(), Status: "waiting"}
	if cli.Store.ID != nil {
		resp.Status = "logged_in"
		resp.LoggedInAs = cli.Store.ID.User
		resp.LoginTime = startTime
	} else {
		resp.Status = "waiting_qr"
		resp.QRFile = "qr.png"
	}
	_ = json.NewEncoder(w).Encode(resp)
}

type sendPayload struct {
	To      string `json:"to"`
	Message string `json:"message"`
}

func sendHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	var p sendPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, `{"error":"bad json"}`, 400)
		return
	}
	jid, err := types.ParseJID(p.To)
	if err != nil {
		http.Error(w, `{"error":"invalid JID"}`, 400)
		return
	}
	_, err = cli.SendMessage(context.Background(), jid, &waProto.Message{Conversation: &p.Message})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	var body string
	_ = json.NewDecoder(r.Body).Decode(&body)
	whMutex.Lock()
	webhookURL = body
	whMutex.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]string{"webhook": webhookURL})
}
