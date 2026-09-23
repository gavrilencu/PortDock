package composefile

import "testing"

func TestServices(t *testing.T) {
	raw := `
services:
  web:
    build: ./frontend
    ports: ["8080:80"]
  api:
    build: ./backend
    expose: ["3000"]
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
	want := []Service{{"api", 3000}, {"bound", 5001}, {"db", 0}, {"multi", 0}, {"web", 80}, {"worker", 9000}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("service %d: got %+v want %+v", i, got[i], want[i])
		}
	}
	if _, err := Services("services: ["); err == nil {
		t.Error("invalid yaml must fail")
	}
}
