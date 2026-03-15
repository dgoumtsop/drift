package proxy
import (
	"net/url"
	"log"
	"net/http"
	"net/http/httputil"
) 
type ReverseProxy struct {
	proxy *httputil.ReverseProxy
}
func New(backendURL string) (*ReverseProxy, error){
	target, err := url.Parse(backendURL)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	return &ReverseProxy{
		proxy: proxy,
	}, nil
}

func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("[PROXY] %s %s -> forwarding to backend", r.Method, r.URL.Path)
	rp.proxy.ServeHTTP(w, r)
}
