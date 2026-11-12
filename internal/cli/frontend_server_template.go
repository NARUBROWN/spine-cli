package cli

const frontendServerHelpers = `
const frontendOutputMarker = ".spine-frontend-output"
const frontendDefaultAPIPrefix = "/api"

type frontendResponseCapture struct {
 response http.ResponseWriter
 header http.Header
 initialHeader http.Header
 status int
 body bytes.Buffer
}
func newFrontendResponseCapture(response http.ResponseWriter) *frontendResponseCapture {
 return &frontendResponseCapture{response: response, initialHeader: cloneFrontendHeader(response.Header())}
}
func (w *frontendResponseCapture) Header() http.Header {
 if w.status == http.StatusNotFound { return w.header }
 return w.response.Header()
}
func (w *frontendResponseCapture) WriteHeader(status int) {
 if w.status != 0 { return }
 if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
  w.response.WriteHeader(status)
  return
 }
 w.status = status
 if status == http.StatusNotFound {
  w.header = cloneFrontendHeader(w.response.Header())
  replaceFrontendHeaderMap(w.response.Header(), w.initialHeader)
  return
 }
 w.response.WriteHeader(status)
}
func (w *frontendResponseCapture) Write(contents []byte) (int, error) {
 if w.status == http.StatusNotFound { return w.body.Write(contents) }
 if w.status == 0 { w.WriteHeader(http.StatusOK) }
 return w.response.Write(contents)
}
func (w *frontendResponseCapture) Flush() {
 if w.status == http.StatusNotFound { return }
 if w.status == 0 { w.WriteHeader(http.StatusOK) }
 if flusher, ok := w.response.(http.Flusher); ok { flusher.Flush() }
}
func (w *frontendResponseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
 if w.status == http.StatusNotFound { return nil, nil, http.ErrNotSupported }
 hijacker, ok := w.response.(http.Hijacker)
 if !ok { return nil, nil, http.ErrNotSupported }
 return hijacker.Hijack()
}
func (w *frontendResponseCapture) Unwrap() http.ResponseWriter { return w.response }
func cloneFrontendHeader(source http.Header) http.Header {
 result := make(http.Header, len(source))
 for key, values := range source { result[key] = append([]string(nil), values...) }
 return result
}
func replaceFrontendHeaderMap(target, source http.Header) {
 for key := range target { delete(target, key) }
 for key, values := range source { target[key] = append([]string(nil), values...) }
}

func frontendAPIPrefix() string {
 prefix := strings.TrimSpace(os.Getenv("SPINE_FRONTEND_API_PREFIX"))
	if !strings.HasPrefix(prefix, "/") || prefix == "/" { return frontendDefaultAPIPrefix }
 return strings.TrimSuffix(prefix, "/")
}
func frontendStaticFallback(root http.FileSystem, apiPrefix string) func(http.Handler) http.Handler {
 if root == nil { return func(next http.Handler) http.Handler { return next } }
 return func(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
   if request.Method != http.MethodGet && request.Method != http.MethodHead { next.ServeHTTP(w, request); return }
   if apiPrefix != "" && (request.URL.Path == apiPrefix || strings.HasPrefix(request.URL.Path, apiPrefix+"/")) { next.ServeHTTP(w, request); return }
   if request.Header.Get("Upgrade") != "" { next.ServeHTTP(w, request); return }
   wantsHTML := strings.Contains(request.Header.Get("Accept"), "text/html")
   if !wantsHTML && filepath.Ext(request.URL.Path) == "" { next.ServeHTTP(w, request); return }
   captured := newFrontendResponseCapture(w)
   next.ServeHTTP(captured, request)
   status := captured.status
   if status == 0 { status = http.StatusOK }
   if status != http.StatusNotFound { return }
   if name, ok := frontendRelativePath(request.URL.Path); ok {
    if serveFrontendFile(w, request, root, name) { return }
   }
   if wantsHTML && frontendAcceptsHTML(request) && filepath.Ext(request.URL.Path) == "" && serveFrontendFile(w, request, root, "index.html") { return }
   copyFrontendResponse(w, captured, status, request.Method)
  })
 }
}
func frontendAcceptsHTML(request *http.Request) bool { return strings.Contains(request.Header.Get("Accept"), "text/html") }
func frontendRelativePath(requestPath string) (string, bool) {
 if strings.Contains(requestPath, "\\") { return "", false }
 name := strings.TrimPrefix(requestPath, "/")
 for _, segment := range strings.Split(name, "/") { if segment == "." || segment == ".." { return "", false } }
 clean := filepath.Clean(filepath.FromSlash(name))
 if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) { return "", false }
 if clean == frontendOutputMarker { return "", false }
 return filepath.ToSlash(clean), true
}
func serveFrontendFile(w http.ResponseWriter, request *http.Request, root http.FileSystem, name string) bool {
 file, err := root.Open(name)
 if err != nil { return false }
 defer file.Close()
 info, err := file.Stat()
 if err != nil || info.IsDir() { return false }
 w.Header().Set("Cache-Control", "no-cache")
 http.ServeContent(w, request, info.Name(), info.ModTime(), file)
 return true
}
func copyFrontendResponse(w http.ResponseWriter, captured *frontendResponseCapture, status int, method string) {
 replaceFrontendHeaderMap(w.Header(), captured.header)
 w.WriteHeader(status)
 if method != http.MethodHead { _, _ = w.Write(captured.body.Bytes()) }
}
func replaceFrontendHeaders(w http.ResponseWriter, captured *frontendResponseCapture) {
	replaceFrontendHeaderMap(w.Header(), captured.header)
}
`

const frontendDiskRoot = `
func frontendStaticRoot() http.FileSystem {
 if configured := strings.TrimSpace(os.Getenv("SPINE_STATIC_DIR")); configured != "" { return http.Dir(configured) }
 executable, err := os.Executable()
 if err != nil { return nil }
 return http.Dir(filepath.Join(filepath.Dir(executable), frontendStaticOutput))
}
`

const frontendEmbeddedRoot = `
//go:embed all:` + frontendEmbedDir + `
var frontendAssets embed.FS

func frontendStaticRoot() http.FileSystem {
 root, err := fs.Sub(frontendAssets, "` + frontendEmbedDir + `")
 if err != nil { return nil }
 return http.FS(root)
}
`
