package authorization

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

func TestLoopbackPromptMetadataAndAnswer(t *testing.T) {
	p := Loopback{RedirectURI: "http://localhost:8082/callback", State: "one-state"}
	p.AuthorizationURL = "https://example.test/login?" + url.Values{"redirect_uri": {p.RedirectURI}, "state": {p.State}}.Encode()
	got := make(chan string, 1)
	service, _ := fixture(t)
	_, err := service.Register(Flow{Key: "x/account", Label: "X", Methods: []Method{{ID: "oauth", Label: "Connect"}}, Run: func(s *Session) error {
		answer, err := s.Prompt(s.Context, Prompt{Kind: "text", Message: "Callback", Loopback: &p})
		got <- answer
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	surface := NewSurface(context.Background(), service, nil)
	defer surface.Close()
	view, err := surface.start("x/account", "oauth")
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		raw, _ := json.Marshal(map[string]string{"id": view.ID})
		value, e := surface.Handle(t.Context(), "get", raw)
		if e != nil {
			t.Fatal(e)
		}
		view = value.(View)
		if len(view.Prompts) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(view.Prompts) != 1 || view.Prompts[0].Loopback == nil || *view.Prompts[0].Loopback != p {
		t.Fatalf("missing metadata: %+v", view.Prompts)
	}
	value := p.RedirectURI + "?code=private&state=one-state"
	raw, _ := json.Marshal(map[string]string{"id": view.ID, "promptId": view.Prompts[0].ID, "answer": value})
	if _, err = surface.Handle(context.Background(), "answer", raw); err != nil {
		t.Fatal(err)
	}
	if <-got != value {
		t.Fatal("callback answer changed")
	}
}

func TestValidLoopback(t *testing.T) {
	p := Loopback{RedirectURI: "http://localhost:8082/callback", State: "state"}
	login := func() string {
		return "https://example.test/login?" + url.Values{"redirect_uri": {p.RedirectURI}, "state": {p.State}}.Encode()
	}
	p.AuthorizationURL = login()
	if !validLoopback(p) {
		t.Fatal("valid loopback rejected")
	}
	for _, uri := range []string{"http://localhost:0/callback", "http://localhost:65536/callback", "http://remote.example:8082/callback", "https://localhost:8082/callback", "http://localhost/callback", "http://user@localhost:8082/callback", "http://localhost:8082/callback#fragment"} {
		p.RedirectURI = uri
		p.AuthorizationURL = login()
		if validLoopback(p) {
			t.Fatalf("accepted %s", uri)
		}
	}
	p.RedirectURI = "http://localhost:8082/callback"
	p.AuthorizationURL = login() + "&state=other"
	if validLoopback(p) {
		t.Fatal("duplicate state accepted")
	}
}
