package app

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// localOnlyMiddleware replaces the password gate with a loopback-only boundary.
// Host validation prevents DNS rebinding; Origin and Fetch Metadata prevent a
// web page from using the browser to operate this passwordless local service.
func localOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !isLoopbackHostname(peer) || !isLoopbackAuthority(r.Host) {
			writeError(w, http.StatusForbidden, "仅允许本机直接访问")
			return
		}
		for _, name := range []string{"Forwarded", "Via", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Port", "X-Forwarded-Proto", "X-Real-IP"} {
			if len(r.Header.Values(name)) > 0 {
				writeError(w, http.StatusForbidden, "不支持通过代理访问本机工具")
				return
			}
		}
		if site := strings.ToLower(r.Header.Get("Sec-Fetch-Site")); site != "" && site != "same-origin" && site != "none" {
			writeError(w, http.StatusForbidden, "不允许跨站访问本机工具")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if err != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, r.Host) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				writeError(w, http.StatusForbidden, "请求来源与本机工具不一致")
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHostname(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func isLoopbackAuthority(authority string) bool {
	u, err := url.Parse("http://" + authority)
	if err != nil || u.Host != authority || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), "localhost") || isLoopbackHostname(u.Hostname())
}
