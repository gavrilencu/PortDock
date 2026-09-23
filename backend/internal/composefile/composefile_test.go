package composefile

import "testing"

func TestServices(t *testing.T) {
	raw := `
services:
  web:
    build: ./frontend
    ports: ["8080:80"]
    environment:
      API_URL: https://api.example.com
      DEBUG:
  api:
    build: ./backend
    expose: ["3000"]
    environment:
      - DATABASE_URL=postgres://db/${DB_NAME}
      - TZ
  db:
    image: postgres:17
  worker:
    image: busybox
    ports:
      - target: 9000
        published: 9000
  multi:
    image: nginx
    ports: ["80", "443"]
  bound:
    image: nginx
    ports: ["127.0.0.1:5000:5001/tcp"]
`
	got, err := Services(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []Service{{"api", 3000, nil, false}, {"bound", 5001, nil, false}, {"db", 0, nil, false}, {"multi", 0, nil, false}, {"web", 80, nil, false}, {"worker", 9000, nil, false}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Port != want[i].Port {
			t.Errorf("service %d: got %+v want %+v", i, got[i], want[i])
		}
	}
	if e := got[0].Env; e["DATABASE_URL"] != "postgres://db/${DB_NAME}" || e["TZ"] != "" || len(e) != 2 {
		t.Errorf("list environment: %v", e)
	}
	if e := got[4].Env; e["API_URL"] != "https://api.example.com" || e["DEBUG"] != "" || len(e) != 2 {
		t.Errorf("map environment: %v", e)
	}
	if !got[0].Build || !got[4].Build || got[2].Build {
		t.Errorf("build flags: api=%v web=%v db=%v", got[0].Build, got[4].Build, got[2].Build)
	}
	if got[2].Env != nil {
		t.Errorf("no environment: %v", got[2].Env)
	}
	if _, err := Services("services: ["); err == nil {
		t.Error("invalid yaml must fail")
	}
}
