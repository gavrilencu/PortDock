package executor

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/shipyard/shipyard/internal/secrets"
	"github.com/shipyard/shipyard/internal/store"
)

// Pool hands out (and caches) executors for servers.
type Pool struct {
	db    *store.DB
	box   *secrets.Box
	mu    sync.Mutex
	conns map[int64]*SSH
	sig   map[int64]string // connection signature to detect config changes
}

func NewPool(db *store.DB, box *secrets.Box) *Pool {
	return &Pool{db: db, box: box, conns: map[int64]*SSH{}, sig: map[int64]string{}}
}

func (p *Pool) For(ctx context.Context, srv store.Server) (Executor, error) {
	if srv.IsLocal {
		return Local{}, nil
	}
	if srv.SSHKeyID == 0 {
		return nil, errors.New("server has no SSH key configured")
	}
	key, err := store.ByID[store.SSHKey](ctx, p.db, "ssh_keys", srv.SSHKeyID)
	if err != nil {
		return nil, errors.New("ssh key not found")
	}
	priv, err := p.box.Decrypt(key.PrivateKey)
	if err != nil {
		return nil, err
	}
	sig := srv.Host + "|" + srv.User + "|" + strconv.Itoa(srv.Port) + "|" + secrets.Hash(priv)

	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.conns[srv.ID]; ok && p.sig[srv.ID] == sig {
		return c, nil
	} else if ok {
		_ = c.Close()
	}
	c := NewSSH(SSHTarget{ServerID: srv.ID, Host: srv.Host, Port: srv.Port, User: srv.User, PrivateKey: priv}, hostKeys{p.db})
	p.conns[srv.ID] = c
	p.sig[srv.ID] = sig
	return c, nil
}

// ForID loads the server and returns its executor.
func (p *Pool) ForID(ctx context.Context, serverID int64) (Executor, store.Server, error) {
	srv, err := store.ByID[store.Server](ctx, p.db, "servers", serverID)
	if err != nil {
		return nil, srv, err
	}
	e, err := p.For(ctx, srv)
	return e, srv, err
}

func (p *Pool) Forget(serverID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.conns[serverID]; ok {
		_ = c.Close()
		delete(p.conns, serverID)
	}
}

type hostKeys struct{ db *store.DB }

func (h hostKeys) KnownHostKey(id int64) string {
	return h.db.Setting(context.Background(), hostKeySetting(id))
}
func (h hostKeys) SaveHostKey(id int64, key string) {
	_ = h.db.SetSetting(context.Background(), hostKeySetting(id), key)
}

func hostKeySetting(id int64) string { return "hostkey:" + strconv.FormatInt(id, 10) }

// ResetHostKey clears the pinned host key (e.g. after reinstalling a server).
func (p *Pool) ResetHostKey(ctx context.Context, id int64) error {
	p.Forget(id)
	_, err := p.db.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", hostKeySetting(id))
	return err
}
