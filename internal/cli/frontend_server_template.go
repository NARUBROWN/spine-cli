package cli

const frontendServerHelpers = `
const frontendStaticOutput = "frontend"
const frontendOutputMarker = ".spine-frontend-output"
const frontendDefaultAPIPrefix = "/api"

type frontendResponseCapture struct {
 header http.Header
 status int
 body bytes.Buffer
}
func newFrontendResponseCapture() *frontendResponseCapture { return &frontendResponseCapture{header: make(http.Header)} }
func (w *frontendResponseCapture) Header() http.Header { return w.header }
func (w *frontendResponseCapture) WriteHeader(status int) { if w.status == 0 { w.status = status } }
func (w *frontendResponseCapture) Write(contents []byte) (int, error) {
 if w.status == 0 { w.status = http.StatusOK }
 return w.body.Write(contents)
}

func frontendStaticRoot() string {
 if configured := strings.TrimSpace(os.Getenv("SPINE_STATIC_DIR")); configured != "" { return configured }
 executable, err := os.Executable()
 if err != nil { return "" }
 return filepath.Join(filepath.Dir(executable), frontendStaticOutput)
}
func frontendAPIPrefix() string {
 prefix := strings.TrimSpace(os.Getenv("SPINE_FRONTEND_API_PREFIX"))
	if !strings.HasPrefix(prefix, "/") || prefix == "/" { return frontendDefaultAPIPrefix }
 return strings.TrimSuffix(prefix, "/")
}
func frontendStaticFallback(root, apiPrefix string) func(http.Handler) http.Handler {
 if root == "" { return func(next http.Handler) http.Handler { return next } }
 return func(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
   if request.Method != http.MethodGet && request.Method != http.MethodHead { next.ServeHTTP(w, request); return }
   if apiPrefix != "" && (request.URL.Path == apiPrefix || strings.HasPrefix(request.URL.Path, apiPrefix+"/")) { next.ServeHTTP(w, request); return }
   if request.Header.Get("Upgrade") != "" { next.ServeHTTP(w, request); return }
   wantsHTML := strings.Contains(request.Header.Get("Accept"), "text/html")
   if !wantsHTML && filepath.Ext(request.URL.Path) == "" { next.ServeHTTP(w, request); return }
   captured := newFrontendResponseCapture()
   next.ServeHTTP(captured, request)
   status := captured.status
   if status == 0 { status = http.StatusOK }
   if status != http.StatusNotFound {
    copyFrontendResponse(w, captured, status, request.Method)
    return
   }
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
func serveFrontendFile(w http.ResponseWriter, request *http.Request, root, name string) bool {
 file, err := http.Dir(root).Open(name)
 if err != nil { return false }
 defer file.Close()
 info, err := file.Stat()
 if err != nil || info.IsDir() { return false }
 w.Header().Set("Cache-Control", "no-cache")
 http.ServeContent(w, request, info.Name(), info.ModTime(), file)
 return true
}
func copyFrontendResponse(w http.ResponseWriter, captured *frontendResponseCapture, status int, method string) {
 for key, values := range captured.header { for _, value := range values { w.Header().Add(key, value) } }
 w.WriteHeader(status)
 if method != http.MethodHead { _, _ = w.Write(captured.body.Bytes()) }
}
func replaceFrontendHeaders(w http.ResponseWriter, captured *frontendResponseCapture) {
 headers := w.Header()
 for key := range headers { delete(headers, key) }
 for key, values := range captured.header { headers[key] = append([]string(nil), values...) }
}
`
