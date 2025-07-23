package main

import (
	"bytes"
	"context"
	"encoding/base64"
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
	cli         *whatsmeow.Client
	webhookURLs []string
	whMutex     sync.Mutex
	startTime   = time.Now()
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
	http.HandleFunc("/webhook", webhookHandler) // GET / POST / DELETE
	http.HandleFunc("/qr", qrHandler)
	http.HandleFunc("/logout", logoutHandler)

	go http.ListenAndServe(":8080", nil)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	cli.Disconnect()
}

/* ---------- WhatsApp ---------- */
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

/* ---------- Events ---------- */
func eventHandler(raw interface{}) {
	switch v := raw.(type) {
	case *events.Message:
		if v.Info.IsFromMe {
			return
		}
		decoded := decodeBase64Fields(v)
		go pushWebhook(decoded)
	}
}

/* ---------- Base64 Decoder ---------- */
func decodeBase64Fields(in interface{}) interface{} {
	b, _ := json.Marshal(in)
	var obj interface{}
	_ = json.Unmarshal(b, &obj)
	return decode(obj)
}

func decode(v interface{}) interface{} {
	switch t := v.(type) {
	case string:
		if decoded, err := base64.StdEncoding.DecodeString(t); err == nil {
			var j interface{}
			if json.Unmarshal(decoded, &j) == nil {
				return j
			}
			return string(decoded)
		}
		return t
	case []interface{}:
		for i, val := range t {
			t[i] = decode(val)
		}
	case map[string]interface{}:
		for k, val := range t {
			t[k] = decode(val)
		}
	}
	return v
}

/* ---------- Webhook Push ---------- */
func pushWebhook(payload interface{}) {
	whMutex.Lock()
	defer whMutex.Unlock()
	if len(webhookURLs) == 0 {
		return
	}
	body, _ := json.Marshal(payload)
	for _, url := range webhookURLs {
		go func(u string) {
			http.Post(u, "application/json", bytes.NewReader(body))
		}(url)
	}
}

/* ---------- Handlers ---------- */
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
		resp.QRFile = "/qr"
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

/* ---------- Webhook CRUD ---------- */
func webhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		whMutex.Lock()
		defer whMutex.Unlock()
		_ = json.NewEncoder(w).Encode(map[string][]string{"webhooks": webhookURLs})
	case http.MethodPost:
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
			http.Error(w, `{"error":"bad json"}`, 400)
			return
		}
		whMutex.Lock()
		found := false
		for _, u := range webhookURLs {
			if u == body.URL {
				found = true
				break
			}
		}
		if !found {
			webhookURLs = append(webhookURLs, body.URL)
		}
		whMutex.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"added": body.URL, "total": len(webhookURLs)})
	case http.MethodDelete:
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
			http.Error(w, `{"error":"bad json"}`, 400)
			return
		}
		whMutex.Lock()
		newList := []string{}
		for _, u := range webhookURLs {
			if u != body.URL {
				newList = append(newList, u)
			}
		}
		webhookURLs = newList
		whMutex.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"removed": body.URL, "total": len(webhookURLs)})
	default:
		http.Error(w, `{"error":"method not allowed"}`, 405)
	}
}

func listWebhooksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	whMutex.Lock()
	defer whMutex.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"webhooks": webhookURLs})
}

func qrHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	http.ServeFile(w, r, "qr.png")
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, 405)
		return
	}
	if cli.Store.ID == nil {
		http.Error(w, `{"error":"not logged in"}`, 400)
		return
	}
	if err := cli.Logout(context.Background()); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	_ = os.Remove("qr.png")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "logged out"})
}
