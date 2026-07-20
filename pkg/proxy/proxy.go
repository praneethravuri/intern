package proxy

import (
	"go.uber.org/zap"
	"net/http"
	"net/http/httputil"
)

type MetricsUpdater interface {
	UpdateMetrics(sessionID string, model string, inputTokens int, outputTokens int)
}

type ProxyServer struct {
	log     *zap.SugaredLogger
	updater MetricsUpdater
	proxy   *httputil.ReverseProxy
}

func NewProxyServer(log *zap.SugaredLogger, updater MetricsUpdater) *ProxyServer {
	s := &ProxyServer{
		log:     log,
		updater: updater,
	}

	return s
}

func (s *ProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.proxy.ServeHTTP(w, r)
}
