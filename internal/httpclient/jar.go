package httpclient

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
)

// Jar is one VU's cookie jar (ADR-009). The underlying cookiejar.Jar is
// created only when a response first sets a cookie, so VUs whose
// responses set none allocate no jar, and looking up cookies for a
// request returns at once. Reset forgets every cookie without allocating.
//
// A Jar belongs to one VU and is used only from its goroutine.
type Jar struct {
	cookies *cookiejar.Jar
}

var _ http.CookieJar = (*Jar)(nil)

// SetCookies stores cookies received from u.
func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	if j.cookies == nil {
		// cookiejar.New returns no error for nil options.
		j.cookies, _ = cookiejar.New(nil)
	}
	j.cookies.SetCookies(u, cookies)
}

// Cookies returns the cookies to send to u.
func (j *Jar) Cookies(u *url.URL) []*http.Cookie {
	if j.cookies == nil {
		return nil
	}
	return j.cookies.Cookies(u)
}

// Reset forgets every cookie: the next request starts a new session.
func (j *Jar) Reset() {
	j.cookies = nil
}

// WithJar returns a client for one VU: it shares shared's transport, and
// so its connection pool, but keeps cookies in jar.
func WithJar(shared *http.Client, jar *Jar) *http.Client {
	c := *shared
	c.Jar = jar
	return &c
}
