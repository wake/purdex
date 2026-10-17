package daemon

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"time"
)

const forwardTimeout = 10 * time.Second

// rejectHostParam is the TCP guard of spec §10: forwarding to another host is
// only for the local socket. A TCP request carrying `host` is refused whatever
// it names, so there is no multi-hop and a token holder cannot relay through
// this wwd.
func rejectHostParam(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("host") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "host is not accepted over TCP"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (d *Daemon) peerByAlias(alias string) (PeerAddr, bool) {
	for _, p := range d.opts.Peers {
		if p.Alias == alias {
			return p, true
		}
	}
	return PeerAddr{}, false
}

// forwardTarget says where a socket request for ?host= goes. local is true when
// the request is about this host (no host, or this host's alias).
func (d *Daemon) forwardTarget(r *http.Request) (peer PeerAddr, local bool, status int) {
	host := r.URL.Query().Get("host")
	if host == "" || host == d.opts.Alias {
		return PeerAddr{}, true, 0
	}
	p, ok := d.peerByAlias(host)
	if !ok {
		return PeerAddr{}, false, http.StatusNotFound
	}
	return p, false, 0
}

// forwardGET forwards a body-less GET (see forward).
func (d *Daemon) forwardGET(w http.ResponseWriter, r *http.Request, peer PeerAddr) {
	d.forward(w, r, peer, nil)
}

// forward sends the socket request (same method, path and body) to the peer over
// TCP with the pool token, without `host`, and returns the peer's answer as it
// is: status, content type and body. A peer that cannot be reached is 502
// {host, reason}.
func (d *Daemon) forward(w http.ResponseWriter, r *http.Request, peer PeerAddr, body []byte) {
	q := r.URL.Query()
	q.Del("host")
	u := url.URL{Scheme: "http", Host: peer.HostPort, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: q.Encode()}
	ctx, cancel := context.WithTimeout(r.Context(), forwardTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), rd)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"host": peer.Alias, "reason": err.Error()})
		return
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+d.opts.Token)
	resp, err := d.client().Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"host": peer.Alias, "reason": err.Error()})
		return
	}
	defer resp.Body.Close()
	answer, err := readCapped(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"host": peer.Alias, "reason": err.Error()})
		return
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(answer)
}

func (d *Daemon) client() *http.Client { return d.fwd }
