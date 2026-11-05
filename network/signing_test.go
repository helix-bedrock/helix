package network

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// The server signs in through discovery, says so in its status, and a bad ID stops it starting.
func TestSignInToSignaling(t *testing.T) {
	connected := make(chan string, 1)
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/api/v1.0/discovery/MinecraftDedicatedServer/builds/", func(w http.ResponseWriter, _ *http.Request) {
		b, _ := json.Marshal(map[string]any{"result": map[string]any{"serviceEnvironments": map[string]any{"signaling": map[string]any{"prod": map[string]any{"serviceUri": srv.URL}}}}})
		_, _ = w.Write(b)
	})
	mux.Handle("/ws/", websocket.Server{
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: func(ws *websocket.Conn) {
			connected <- ws.Request().URL.Path
			var text string
			for websocket.Message.Receive(ws, &text) == nil {
			}
		},
	})
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const id = "12345678-1234-1234-1234-123456789abc"
	conf := NetherNetConfig{Address: "127.0.0.1:0", IdentityKey: key, SignInID: id, DiscoveryURL: srv.URL, CertificatePath: "no-such-file", Log: slog.New(slog.DiscardHandler)}
	tr, err := NetherNet(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()
	server := tr.(*netherNetTransport)
	server.SetStatus(Status{Name: "a"})

	select {
	case p := <-connected:
		if p != "/ws/v1.0/messaging/connect/20CA2/"+id {
			t.Errorf("path %s", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not sign in")
	}
	want := `"connection":` + strconv.Itoa(connectionSignedIn)
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := httptest.NewRecorder()
		server.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/join", nil))
		if strings.Contains(rec.Body.String(), want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status %s", rec.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	conf.SignInID = "not-a-uuid"
	if bad, err := NetherNet(conf); err == nil {
		_ = bad.Close()
		t.Error("started with a bad nethernet-id")
	}
}
