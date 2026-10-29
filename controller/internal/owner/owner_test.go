package owner

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestOnlyOneOwnerAndErrorsRefuse(t *testing.T) {
	root := t.TempDir()
	a, e := Acquire(root)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if b, e := Acquire(root); e == nil {
		b.Close()
		t.Fatal("duplicate owner acquired")
	}
	bad := filepath.Join(root, "not-directory")
	os.WriteFile(bad, []byte("x"), 0600)
	if b, e := Acquire(bad); e == nil {
		b.Close()
		t.Fatal("filesystem error failed open")
	}
}
func TestLocalCommandBoundary(t *testing.T) {
	root := t.TempDir()
	calls := 0
	s, e := Listen(root, func() any { return map[string]int{"calls": calls} }, func(c string) error { calls++; return nil })
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	b, _ := os.ReadFile(filepath.Join(root, "endpoint.json"))
	var ep Endpoint
	json.Unmarshal(b, &ep)
	res, e := http.Get(ep.URL + "/status")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("unauthorized status exposed")
	}
	if _, e = Call(root, "start"); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("command not routed")
	}
	req, _ := http.NewRequest("POST", ep.URL+"/command", nil)
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	req.Header.Set("Origin", "http://example.test")
	res, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 || calls != 1 {
		t.Fatal("browser origin command accepted")
	}
}
