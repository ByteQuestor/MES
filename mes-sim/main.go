// mes-sim —— MES 电池整盘信息仿真系统
// 单文件部署：前端页面通过 go:embed 内嵌，编译后为单个可执行文件。
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed all:web
var webFS embed.FS

const version = "1.0.0"

// ---------------- 数据模型 ----------------

// Material 框内单个物料（如一颗电池）
type Material struct {
	Barcode string `json:"barcode"`
	Status  string `json:"status"`
}

// Tray 一盘料：一个框 + 框内物料列表
type Tray struct {
	ID         string     `json:"id"`
	BoxBarcode string     `json:"boxBarcode"`
	Remark     string     `json:"remark,omitempty"`
	Materials  []Material `json:"materials"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// TrayInput 新建/编辑料盘时的请求体
type TrayInput struct {
	BoxBarcode string     `json:"boxBarcode"`
	Remark     string     `json:"remark"`
	Materials  []Material `json:"materials"`
}

// QueryLog 一次 WCS 查询的记录
type QueryLog struct {
	Time       time.Time `json:"time"`
	Barcode    string    `json:"barcode"`
	Found      bool      `json:"found"`
	MatchType  string    `json:"matchType,omitempty"` // box / material
	BoxBarcode string    `json:"boxBarcode,omitempty"`
	ClientIP   string    `json:"clientIp,omitempty"`
}

// QueryResult /api/query 的响应数据
type QueryResult struct {
	Found          bool   `json:"found"`
	MatchType      string `json:"matchType,omitempty"`
	MatchedBarcode string `json:"matchedBarcode,omitempty"`
	Tray           *Tray  `json:"tray,omitempty"`
}

// ---------------- 存储（内存 + JSON 文件持久化） ----------------

const maxLogs = 500

type Store struct {
	mu   sync.Mutex
	file string
	Data struct {
		Trays []Tray     `json:"trays"`
		Logs  []QueryLog `json:"logs"`
	}
}

func NewStore(file string) *Store {
	s := &Store{file: file}
	raw, err := os.ReadFile(file)
	if err != nil {
		return s // 首次启动，尚无数据文件
	}
	if err := json.Unmarshal(raw, &s.Data); err != nil {
		log.Printf("[警告] 数据文件 %s 解析失败，将以空数据启动: %v", file, err)
	}
	return s
}

// saveLocked 写盘（临时文件 + 原子替换），调用前须已持有 mu
func (s *Store) saveLocked() {
	b, err := json.MarshalIndent(s.Data, "", "  ")
	if err != nil {
		log.Printf("[错误] 数据序列化失败: %v", err)
		return
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("[错误] 写入数据文件失败: %v", err)
		return
	}
	if err := os.Rename(tmp, s.file); err != nil {
		log.Printf("[错误] 保存数据文件失败: %v", err)
	}
}

func newID() string {
	return fmt.Sprintf("%d%04d", time.Now().UnixNano(), rand.Intn(10000))
}

func cleanInput(in TrayInput) (TrayInput, error) {
	in.BoxBarcode = strings.TrimSpace(in.BoxBarcode)
	in.Remark = strings.TrimSpace(in.Remark)
	if in.BoxBarcode == "" {
		return in, fmt.Errorf("框体条码不能为空")
	}
	mats := make([]Material, 0, len(in.Materials))
	seen := make(map[string]bool, len(in.Materials))
	for i, m := range in.Materials {
		bc, st := strings.TrimSpace(m.Barcode), strings.TrimSpace(m.Status)
		if bc == "" {
			return in, fmt.Errorf("第 %d 个物料的条码不能为空", i+1)
		}
		if seen[bc] {
			return in, fmt.Errorf("第 %d 个物料条码重复: %s", i+1, bc)
		}
		seen[bc] = true
		mats = append(mats, Material{Barcode: bc, Status: st})
	}
	in.Materials = mats
	return in, nil
}

func (s *Store) CreateTray(in TrayInput) (Tray, error) {
	in, err := cleanInput(in)
	if err != nil {
		return Tray{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Data.Trays {
		if s.Data.Trays[i].BoxBarcode == in.BoxBarcode {
			return Tray{}, fmt.Errorf("框体条码 %s 已存在", in.BoxBarcode)
		}
	}
	now := time.Now()
	t := Tray{
		ID:         newID(),
		BoxBarcode: in.BoxBarcode,
		Remark:     in.Remark,
		Materials:  in.Materials,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s.Data.Trays = append(s.Data.Trays, t)
	s.saveLocked()
	return t, nil
}

func (s *Store) UpdateTray(id string, in TrayInput) (Tray, error) {
	in, err := cleanInput(in)
	if err != nil {
		return Tray{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i := range s.Data.Trays {
		if s.Data.Trays[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Tray{}, fmt.Errorf("料盘不存在或已被删除")
	}
	for i := range s.Data.Trays {
		if i != idx && s.Data.Trays[i].BoxBarcode == in.BoxBarcode {
			return Tray{}, fmt.Errorf("框体条码 %s 已存在于其它料盘", in.BoxBarcode)
		}
	}
	t := &s.Data.Trays[idx]
	t.BoxBarcode = in.BoxBarcode
	t.Remark = in.Remark
	t.Materials = in.Materials
	t.UpdatedAt = time.Now()
	out := *t
	s.saveLocked()
	return out, nil
}

func (s *Store) DeleteTray(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Data.Trays {
		if s.Data.Trays[i].ID == id {
			s.Data.Trays = append(s.Data.Trays[:i], s.Data.Trays[i+1:]...)
			s.saveLocked()
			return nil
		}
	}
	return fmt.Errorf("料盘不存在或已被删除")
}

// QueryAndLog 按“先框体条码、后物料条码”的顺序查找；每次查询（含未命中）都记日志。
// 同一条码出现在多盘时返回创建最早的一盘。
func (s *Store) QueryAndLog(code, ip string) QueryResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := QueryResult{}
	entry := QueryLog{Time: time.Now(), Barcode: code, ClientIP: ip}

	for i := range s.Data.Trays {
		if s.Data.Trays[i].BoxBarcode == code {
			t := s.Data.Trays[i]
			res = QueryResult{Found: true, MatchType: "box", MatchedBarcode: code, Tray: &t}
			break
		}
	}
	if !res.Found {
		for i := range s.Data.Trays {
			for _, m := range s.Data.Trays[i].Materials {
				if m.Barcode == code {
					t := s.Data.Trays[i]
					res = QueryResult{Found: true, MatchType: "material", MatchedBarcode: code, Tray: &t}
					break
				}
			}
			if res.Found {
				break
			}
		}
	}

	if res.Found {
		entry.Found = true
		entry.MatchType = res.MatchType
		entry.BoxBarcode = res.Tray.BoxBarcode
	}
	s.Data.Logs = append([]QueryLog{entry}, s.Data.Logs...)
	if len(s.Data.Logs) > maxLogs {
		s.Data.Logs = s.Data.Logs[:maxLogs]
	}
	s.saveLocked()
	return res
}

// ---------------- HTTP 处理器 ----------------

type apiResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func ok(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(apiResp{Code: 0, Message: "ok", Data: data})
}

func fail(w http.ResponseWriter, httpStatus int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(apiResp{Code: 1, Message: msg})
}

func (s *Store) handleTrays(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.Data.Trays == nil {
			s.Data.Trays = []Tray{}
		}
		ok(w, s.Data.Trays)
	case http.MethodPost:
		var in TrayInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
			return
		}
		t, err := s.CreateTray(in)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		ok(w, t)
	default:
		fail(w, http.StatusMethodNotAllowed, "不支持的方法 "+r.Method)
	}
}

func (s *Store) handleTrayByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/trays/")
	if id == "" {
		fail(w, http.StatusNotFound, "缺少料盘 ID")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var in TrayInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			fail(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
			return
		}
		t, err := s.UpdateTray(id, in)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		ok(w, t)
	case http.MethodDelete:
		if err := s.DeleteTray(id); err != nil {
			fail(w, http.StatusNotFound, err.Error())
			return
		}
		ok(w, nil)
	default:
		fail(w, http.StatusMethodNotAllowed, "不支持的方法 "+r.Method)
	}
}

// extractBarcode 兼容 GET 参数与 POST（JSON/表单），参数名兼容 barcode/barCode/code
func extractBarcode(r *http.Request) string {
	code := r.URL.Query().Get("barcode")
	if code == "" {
		code = r.URL.Query().Get("code")
	}
	if r.Method == http.MethodPost && code == "" {
		if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			var body struct {
				Barcode string `json:"barcode"`
				BarCode string `json:"barCode"`
				Code    string `json:"code"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				code = firstNonEmpty(body.Barcode, body.BarCode, body.Code)
			}
		} else {
			code = firstNonEmpty(r.PostFormValue("barcode"), r.PostFormValue("barCode"), r.PostFormValue("code"))
		}
	}
	return strings.TrimSpace(code)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *Store) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "不支持的方法 "+r.Method)
		return
	}
	code := extractBarcode(r)
	if code == "" {
		fail(w, http.StatusBadRequest, "缺少条码参数（barcode）")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	ok(w, s.QueryAndLog(code, ip))
}

func (s *Store) handleLogs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		if s.Data.Logs == nil {
			s.Data.Logs = []QueryLog{}
		}
		ok(w, s.Data.Logs)
	case http.MethodDelete:
		s.Data.Logs = []QueryLog{}
		s.saveLocked()
		ok(w, nil)
	default:
		fail(w, http.StatusMethodNotAllowed, "不支持的方法 "+r.Method)
	}
}

func (s *Store) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ok(w, map[string]any{
		"app":          "mes-sim",
		"version":      version,
		"trays":        len(s.Data.Trays),
		"logs":         len(s.Data.Logs),
		"dataFile":     s.file,
		"dataFileName": filepath.Base(s.file),
		"time":         time.Now(),
	})
}

// ---------------- 中间件 ----------------

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s -> %d (%s)", r.Method, r.URL.RequestURI(), sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

// ---------------- 启动 ----------------

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("无法自动打开浏览器: %v", err)
	}
}

func localIPs() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

func main() {
	port := flag.Int("port", 8080, "HTTP 监听端口")
	open := flag.Bool("open", true, "启动后自动打开浏览器")
	dataFile := flag.String("data", "", "数据文件路径（默认为 exe 同目录下的 mes-data.json）")
	flag.Parse()
	if p := os.Getenv("MES_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			*port = v
		}
	}

	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	if *dataFile == "" {
		*dataFile = filepath.Join(exeDir, "mes-data.json")
	}
	if dir := filepath.Dir(*dataFile); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("数据目录不可用: %v", err)
		}
	}

	store := NewStore(*dataFile)
	log.Printf("数据文件: %s（共 %d 盘料，%d 条查询日志）", *dataFile, len(store.Data.Trays), len(store.Data.Logs))

	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("内嵌前端资源异常: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/trays", store.handleTrays)
	mux.HandleFunc("/api/trays/", store.handleTrayByID)
	mux.HandleFunc("/api/query", store.handleQuery)
	mux.HandleFunc("/api/logs", store.handleLogs)
	mux.HandleFunc("/api/health", store.handleHealth)
	mux.Handle("/", http.FileServer(http.FS(webRoot)))

	addr := fmt.Sprintf(":%d", *port)
	url := fmt.Sprintf("http://localhost:%d", *port)
	log.Printf("==================================================")
	log.Printf("  MES 电池整盘信息仿真系统 v%s 已启动", version)
	log.Printf("  本机访问:   %s", url)
	for _, ip := range localIPs() {
		log.Printf("  局域网访问: http://%s:%d   （WCS 查询: /api/query?barcode=条码）", ip, *port)
	}
	log.Printf("==================================================")
	if *open {
		openBrowser(url)
	}
	if err := http.ListenAndServe(addr, accessLog(cors(mux))); err != nil {
		log.Fatalf("HTTP 服务启动失败（端口 %d 可能被占用，可用 -port 换端口）: %v", *port, err)
	}
}
