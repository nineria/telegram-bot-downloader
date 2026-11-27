// Saves every file forwarded to the bot into DOWNLOAD_DIR.
//
// The poller turns each forwarded file into an Inngest event. An Inngest function does the download:
// it is queued, limited to PARALLEL_DOWNLOADS at a time, retried, and survives restarts (the Inngest server
// keeps the queue). Needs the local Bot API server (files over 20MB) and an Inngest server.
// shortcut: Inngest does not keep forwarding order; add a priority expression if order matters.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/connect"
	inngesterrors "github.com/inngest/inngestgo/errors"
	"github.com/inngest/inngestgo/step"
)

type media struct {
	FileID   string `json:"file_id"`
	UniqueID string `json:"file_unique_id"` // stable across forwards; used to skip duplicates
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

type message struct {
	MessageID int                `json:"message_id"`
	Text      string             `json:"text"`
	GroupID   string             `json:"media_group_id"` // set for albums
	ReplyTo   *message           `json:"reply_to_message"`
	From      struct{ ID int64 } `json:"from"`
	Chat      struct{ ID int64 } `json:"chat"`
	Document  *media             `json:"document"`
	Video     *media             `json:"video"`
	Audio     *media             `json:"audio"`
	Voice     *media             `json:"voice"`
	Animation *media             `json:"animation"`
	VideoNote *media             `json:"video_note"`
	Photo     []media            `json:"photo"`
}

const (
	appID         = "macdrop"
	fnID          = "download-file"
	eventName     = "tg/file.received"
	pingEvent     = "macdrop/ping" // health check: proves Inngest still runs our functions
	defParallel   = 2              // simultaneous downloads (Inngest function concurrency)
	defMaxRetries = 8              // per step; each retry re-calls getFile, which resumes the partial file
)

var (
	token, dest, apiURL, localRoot string
	client                         inngestgo.Client
)

// fileEvent is the Inngest event payload: the forwarded message, its file, and the bot's status message.
type fileEvent struct {
	M      *message `json:"m"`
	A      *media   `json:"a"`
	Status int      `json:"status"`
}

type job struct {
	M *message
	A *media
}

// state in failed.json. Pending is only read once, to hand a previous version's queue to Inngest.
type pendingTask struct {
	M  *message
	A  *media
	ID int
}

var (
	mu     sync.Mutex
	failed = map[int]job{}       // by message_id
	done   = map[string]string{} // file_unique_id -> saved path, so a re-forwarded file is skipped
	origin = map[int]*message{}  // bot status message_id -> the file's message
	// shortcut: progress is read from the local server's temp dir, so parallel downloads share one percentage.
	active   int
	baseline int64
	expected int64
	// shortcut: /status counts only this session; jobs Inngest re-runs after a restart show once they start.
	waiting     = map[int]string{} // queued, not started
	downloading = map[int]string{} // by message_id
	doneCount   int
)

const stateFile = "failed.json"

// persist saves the failed list; the caller holds mu.
func persist() {
	b, _ := json.Marshal(struct {
		Failed map[int]job
		Origin map[int]*message
		Done   map[string]string
	}{failed, origin, done})
	os.WriteFile(stateFile, b, 0o600)
}

func loadState() (pending map[int]pendingTask) {
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return nil
	}
	var st struct {
		Failed  map[int]job
		Origin  map[int]*message
		Done    map[string]string
		Pending map[int]pendingTask
	}
	if json.Unmarshal(b, &st) == nil {
		if st.Failed != nil {
			failed = st.Failed
		}
		if st.Origin != nil {
			origin = st.Origin
		}
		if st.Done != nil {
			done = st.Done
		}
	}
	return st.Pending
}

var errNet = errors.New("network error")

// lastPong is when the worker last handled a health-check event (unix nanos).
var lastPong atomic.Int64

func pong(ctx context.Context, in inngestgo.Input[struct{}]) (any, error) {
	lastPong.Store(time.Now().UnixNano())
	return nil, nil
}

func api(method string, params url.Values, out any) (rerr error) {
	start := time.Now()
	defer func() {
		if method != "getUpdates" { // long-poll would flood the log
			log.Printf("api %s took %s err=%v", method, time.Since(start).Round(time.Millisecond), rerr)
		}
	}()
	r, err := http.PostForm(apiURL+"/bot"+token+"/"+method, params)
	if err != nil {
		return fmt.Errorf("%s: %w", method, errNet) // url.Error would leak the token
	}
	defer r.Body.Close()
	var resp struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Description)
	}
	if out != nil {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}

func reply(chat int64, text string) int {
	var sent struct {
		MessageID int `json:"message_id"`
	}
	api("sendMessage", url.Values{"chat_id": {strconv.FormatInt(chat, 10)}, "text": {text}}, &sent)
	return sent.MessageID
}

func edit(chat int64, id int, text string) {
	api("editMessageText", url.Values{"chat_id": {strconv.FormatInt(chat, 10)}, "message_id": {strconv.Itoa(id)}, "text": {text}}, nil)
}

// statusText summarises the queue for the /status command.
func statusText() string {
	mu.Lock()
	defer mu.Unlock()
	names := func(m map[int]string) string {
		ids := make([]int, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		var b strings.Builder
		for _, id := range ids {
			b.WriteString("\n  • " + m[id])
		}
		return b.String()
	}
	return fmt.Sprintf("📊 Status\n⬇️ downloading: %d%s\n⏳ waiting: %d%s\n❌ failed: %d (reply /retry)\n✅ done since start: %d",
		len(downloading), names(downloading), len(waiting), names(waiting), len(failed), doneCount)
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
}

func dur(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Minute {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func bar(p int) string {
	n := p * 12 / 100
	return strings.Repeat("🟩", n) + strings.Repeat("⬜", 12-n)
}

func fileName(a *media) string {
	if a.FileName == "" {
		return "file"
	}
	return a.FileName
}

func attachment(m *message) *media {
	for _, a := range []*media{m.Document, m.Video, m.Audio, m.Voice, m.Animation, m.VideoNote} {
		if a != nil {
			return a
		}
	}
	if n := len(m.Photo); n > 0 {
		return &m.Photo[n-1] // largest size
	}
	return nil
}

func tempBytes() (n int64) {
	es, _ := os.ReadDir(filepath.Join(localRoot, token, "temp"))
	for _, e := range es {
		if i, err := e.Info(); err == nil {
			n += i.Size()
		}
	}
	return n
}

// alreadyHave reports the saved path of a file with the same Telegram unique ID, if it is still on disk.
func alreadyHave(a *media) (string, bool) {
	if a.UniqueID == "" {
		return "", false
	}
	mu.Lock()
	p := done[a.UniqueID]
	mu.Unlock()
	if p == "" {
		return "", false
	}
	_, err := os.Stat(p)
	return p, err == nil
}

// enqueue posts the "queued" message and hands the file to Inngest. id is the event ID, which
// Inngest uses to drop duplicates; a retry passes a fresh one.
func enqueue(m *message, a *media, id string) {
	if p, ok := alreadyHave(a); ok {
		reply(m.Chat.ID, "⏭ Already downloaded, skipped:\n"+p+"\n(delete that file to download it again)")
		return
	}
	status := reply(m.Chat.ID, fmt.Sprintf("⏳ %s\n%s · queued", fileName(a), human(a.FileSize)))
	if err := send(m, a, status, id); err != nil {
		log.Printf("queue FAILED msg=%d: %v", m.MessageID, err)
		edit(m.Chat.ID, status, fmt.Sprintf("❌ %s\nCould not queue it: %v\n\n↩️ Reply /retry to this message to try again.", fileName(a), err))
		mu.Lock()
		failed[m.MessageID] = job{m, a}
		origin[status] = m
		persist()
		mu.Unlock()
		return
	}
	mu.Lock()
	waiting[m.MessageID] = fileName(a)
	mu.Unlock()
	log.Printf("queued msg=%d name=%q size=%d", m.MessageID, a.FileName, a.FileSize)
}

func send(m *message, a *media, status int, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := client.Send(ctx, inngestgo.Event{
		ID:   &id,
		Name: eventName,
		Data: map[string]any{"m": m, "a": a, "status": status},
	})
	return err
}

// fetch is the long step: getFile makes the local server download the whole file. The server cuts
// a call off after ~500s ("network error") but keeps the partial file, so a retry resumes it.
func fetch(e fileEvent) (string, error) {
	m, a := e.M, e.A
	started := time.Now()
	mu.Lock()
	if active == 0 {
		baseline = tempBytes()
	}
	active++
	expected += a.FileSize
	delete(waiting, m.MessageID)
	downloading[m.MessageID] = fileName(a)
	mu.Unlock()
	done := make(chan struct{})
	defer func() {
		close(done)
		mu.Lock()
		active--
		expected -= a.FileSize
		delete(downloading, m.MessageID)
		mu.Unlock()
	}()

	go func() {
		last := -1
		for {
			select {
			case <-done:
				return
			case <-time.After(2 * time.Second):
			}
			mu.Lock()
			got, total, n := tempBytes()-baseline, expected, active
			mu.Unlock()
			log.Printf("progress msg=%d temp=%d/%d bytes (%s)", m.MessageID, got, total, time.Since(started).Round(time.Second))
			if total <= 0 || got <= 0 {
				continue
			}
			p := int(min(99, got*100/total))
			if p == last {
				continue
			}
			last = p
			el := time.Since(started)
			speed := float64(got) / el.Seconds()
			eta := "?"
			if speed > 0 {
				eta = dur(time.Duration(float64(total-got) / speed * float64(time.Second)))
			}
			scope := ""
			if n > 1 {
				scope = fmt.Sprintf(" (%d files together)", n)
			}
			edit(m.Chat.ID, e.Status, fmt.Sprintf("⬇️ %s\n%s %d%%\n📦 %s / %s%s\n🚀 %.1f MB/s  ⏳ ETA %s  ⏱ %s",
				fileName(a), bar(p), p, human(got), human(total), scope, speed/(1<<20), eta, dur(el)))
		}
	}()

	var f struct {
		FilePath string `json:"file_path"`
	}
	if err := api("getFile", url.Values{"file_id": {a.FileID}}, &f); err != nil {
		if errors.Is(err, errNet) {
			log.Printf("getFile cut off for msg=%d after %s, Inngest will retry (resume)", m.MessageID, time.Since(started).Round(time.Second))
			return "", err
		}
		return "", inngesterrors.NoRetryError(err) // e.g. bad file id: retrying cannot help
	}
	if !strings.HasPrefix(f.FilePath, "/") {
		return "", inngesterrors.NoRetryError(errors.New("the Bot API server is not local, so it cannot hand over files"))
	}
	el := time.Since(started)
	log.Printf("fetched msg=%d in %s avg %.1f MB/s", m.MessageID, el.Round(time.Millisecond), float64(a.FileSize)/(1<<20)/el.Seconds())
	return filepath.Join(localRoot, strings.TrimPrefix(f.FilePath, "/var/lib/telegram-bot-api")), nil
}

// safeName replaces characters Windows forbids in file names, so the same name saves on every OS.
// shortcut: reserved Windows names (CON, NUL, ...) and trailing dots are not handled; add if anyone hits it.
func safeName(n string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, n)
}

// move renames src to dst, or copies and deletes when they are on different disks.
func move(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part" // a half-copied file never looks finished
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Close()
	} else {
		out.Close()
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// finish moves the downloaded file into place. Safe to repeat: if an earlier attempt already moved it, it is done.
func finish(e fileEvent, src string) (string, error) {
	m, a := e.M, e.A
	name := safeName(filepath.Base(fileName(a)))
	if filepath.Ext(name) == "" {
		name += filepath.Ext(src) // photos have no name; the server's path carries the real extension (.jpg)
	}
	path := filepath.Join(dest, fmt.Sprintf("%d_%s", m.MessageID, name)) // id prefix avoids overwrites
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		if err := move(src, path); err != nil {
			return "", err
		}
	}
	edit(m.Chat.ID, e.Status, fmt.Sprintf("✅ %s\n🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩 100%%\n📦 %s\n📁 %s", fileName(a), human(a.FileSize), path))
	mu.Lock()
	doneCount++
	if a.UniqueID != "" {
		done[a.UniqueID] = path
		persist()
	}
	mu.Unlock()
	log.Printf("download OK msg=%d -> %s", m.MessageID, path)
	return path, nil
}

func downloadFile(ctx context.Context, in inngestgo.Input[fileEvent]) (any, error) {
	e := in.Event.Data
	log.Printf("run start msg=%d attempt=%d", e.M.MessageID, in.InputCtx.Attempt)
	if _, err := step.Run(ctx, "start", func(ctx context.Context) (bool, error) {
		edit(e.M.Chat.ID, e.Status, fmt.Sprintf("⬇️ %s\n%s · starting…", fileName(e.A), human(e.A.FileSize)))
		return true, nil
	}); err != nil {
		return nil, err
	}
	src, err := step.Run(ctx, "download", func(ctx context.Context) (string, error) { return fetch(e) })
	if err != nil {
		return nil, err
	}
	return step.Run(ctx, "save", func(ctx context.Context) (string, error) { return finish(e, src) })
}

// onFailure runs when a download has used up its retries: it keeps the file for /retry and tells the user.
func onFailure(ctx context.Context, in inngestgo.Input[struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	Event struct {
		Data fileEvent `json:"data"`
	} `json:"event"`
}]) (any, error) {
	e := in.Event.Data.Event.Data
	return step.Run(ctx, "record", func(ctx context.Context) (bool, error) {
		log.Printf("download FAILED msg=%d: %s", e.M.MessageID, in.Event.Data.Error.Message)
		edit(e.M.Chat.ID, e.Status, fmt.Sprintf("❌ %s\n%s\nReason: %s\n\n↩️ Reply /retry to this message to retry it (and its whole album).\n/retry alone retries every failed file.",
			fileName(e.A), human(e.A.FileSize), in.Event.Data.Error.Message))
		mu.Lock()
		failed[e.M.MessageID] = job{e.M, e.A}
		origin[e.Status] = e.M // lets a reply to this status message find its file
		persist()
		mu.Unlock()
		return true, nil
	})
}

// retryFailed re-queues failed files: all of them, or only those matching target (the file the
// user replied /retry to; its whole album if it has one).
func retryFailed(chat int64, target *message) {
	mu.Lock()
	jobs := map[int]job{}
	for id, j := range failed {
		if target == nil || id == target.MessageID || (target.GroupID != "" && j.M.GroupID == target.GroupID) {
			jobs[id] = j
			delete(failed, id)
		}
	}
	persist()
	mu.Unlock()
	if len(jobs) == 0 && target != nil {
		// not in the failed list: queue the replied-to file anyway
		if a := attachment(target); a != nil {
			reply(chat, "retrying 1 file(s)")
			enqueue(target, a, fmt.Sprintf("tg-%d-%d", target.MessageID, time.Now().UnixMilli()))
			return
		}
	}
	if len(jobs) == 0 {
		reply(chat, "nothing to retry")
		return
	}
	reply(chat, fmt.Sprintf("retrying %d file(s)", len(jobs)))
	ids := make([]int, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		enqueue(jobs[id].M, jobs[id].A, fmt.Sprintf("tg-%d-%d", id, time.Now().UnixMilli()))
	}
}

// envInt reads a positive integer setting, falling back to def when it is unset or invalid.
func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	if v := os.Getenv(key); v != "" {
		log.Printf("ignoring %s=%q (want a positive number), using %d", key, v, def)
	}
	return def
}

func loadEnv() {
	dir, _ := os.Executable()
	for _, p := range []string{".env", filepath.Join(filepath.Dir(dir), ".env")} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
			if ok && !strings.HasPrefix(k, "#") && os.Getenv(k) == "" {
				os.Setenv(k, strings.TrimSpace(v))
			}
		}
	}
}

func main() {
	loadEnv()
	if f, err := os.OpenFile("macdrop.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, f)) // also keep a file: a supervisor's log view may be a limited buffer
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}
	pending := loadState()
	token = os.Getenv("TELEGRAM_BOT_TOKEN")
	owner, err := strconv.ParseInt(os.Getenv("TELEGRAM_OWNER_ID"), 10, 64)
	if token == "" || err != nil {
		log.Fatal("set TELEGRAM_BOT_TOKEN and numeric TELEGRAM_OWNER_ID in .env")
	}
	if apiURL = os.Getenv("TELEGRAM_API_URL"); apiURL == "" {
		log.Fatal("set TELEGRAM_API_URL to the local Bot API server (files over 20MB need it)")
	}
	if localRoot = os.Getenv("LOCAL_API_ROOT"); localRoot == "" {
		log.Fatal("set LOCAL_API_ROOT to the local Bot API server's data folder")
	}
	for _, k := range []string{"INNGEST_BASE_URL", "INNGEST_DEV"} {
		if os.Getenv(k) == "" {
			log.Fatalf("set %s in .env", k)
		}
	}
	if dest = os.Getenv("DOWNLOAD_DIR"); dest == "" {
		dest = "~/Downloads/telegram"
	}
	if strings.HasPrefix(dest, "~/") {
		home, _ := os.UserHomeDir()
		dest = filepath.Join(home, dest[2:])
	}

	parallel, maxRetries := envInt("PARALLEL_DOWNLOADS", defParallel), envInt("MAX_RETRIES", defMaxRetries)
	log.Printf("config: parallel=%d retries=%d dest=%s", parallel, maxRetries, dest)
	client, err = inngestgo.NewClient(inngestgo.ClientOpts{AppID: appID})
	if err != nil {
		log.Fatal(err)
	}
	if _, err = inngestgo.CreateFunction(client, inngestgo.FunctionOpts{
		ID:          fnID,
		Concurrency: &inngestgo.ConfigConcurrency{Fn: []inngestgo.ConfigFnConcurrency{{Limit: parallel}}},
		Retries:     inngestgo.IntPtr(maxRetries),
	}, inngestgo.EventTrigger(eventName, nil), downloadFile); err != nil {
		log.Fatal(err)
	}
	if _, err = inngestgo.CreateFunction(client, inngestgo.FunctionOpts{ID: "on-failure"},
		inngestgo.EventTrigger("inngest/function.failed",
			inngestgo.StrPtr(fmt.Sprintf("event.data.function_id == '%s-%s'", appID, fnID))), onFailure); err != nil {
		log.Fatal(err)
	}
	if _, err = inngestgo.CreateFunction(client, inngestgo.FunctionOpts{ID: "health-ping", Retries: inngestgo.IntPtr(0)},
		inngestgo.EventTrigger(pingEvent, nil), pong); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The Inngest server advertises its gateway as localhost; inside Docker it is another host (INNGEST_GATEWAY_HOST=inngest:8289).
	var rewrite func(url.URL) (url.URL, error)
	if h := os.Getenv("INNGEST_GATEWAY_HOST"); h != "" {
		rewrite = func(u url.URL) (url.URL, error) { u.Host = h; return u, nil }
	}
	conn, err := inngestgo.Connect(ctx, inngestgo.ConnectOpts{Apps: []inngestgo.Client{client}, MaxWorkerConcurrency: new(int64(8)), RewriteGatewayEndpoint: rewrite})
	if err != nil {
		log.Fatal("inngest connect: ", err)
	}
	// The SDK retries a lost connection itself; if it stays down, exit so a process supervisor restarts us.
	go func() {
		var downSince time.Time
		last := conn.State()
		for range time.Tick(5 * time.Second) {
			st := conn.State()
			if st != last {
				log.Printf("inngest connection %s -> %s", last, st)
				last = st
			}
			switch {
			case st == connect.ConnectionStateActive:
				downSince = time.Time{}
			case downSince.IsZero():
				downSince = time.Now()
			case time.Since(downSince) > 2*time.Minute:
				log.Fatalf("inngest connection %s for over 2m, exiting to restart", st)
			}
		}
	}()
	// A connection can look healthy while Inngest no longer runs our functions (e.g. after the Inngest server
	// restarted). Send a ping event every minute; if none is handled for 5 minutes, exit so we get restarted.
	lastPong.Store(time.Now().UnixNano())
	go func() {
		for range time.Tick(time.Minute) {
			pctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err := client.Send(pctx, inngestgo.Event{Name: pingEvent})
			cancel()
			if err != nil {
				log.Printf("health ping not sent: %v", err)
			}
			if idle := time.Since(time.Unix(0, lastPong.Load())); idle > 5*time.Minute {
				log.Fatalf("no health ping handled for %s, exiting to restart", idle.Round(time.Second))
			}
		}
	}()
	go func() { // let in-flight steps hand over cleanly so Inngest retries them right away
		<-ctx.Done()
		conn.Close()
		os.Exit(0)
	}()

	// A previous version kept its queue in this file; hand it to Inngest once.
	ids := make([]int, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		t := pending[id]
		log.Printf("handing msg=%d over to Inngest", id)
		if err := send(t.M, t.A, t.ID, fmt.Sprintf("tg-%d-handover", id)); err != nil {
			log.Printf("handover FAILED msg=%d: %v", id, err)
			mu.Lock()
			failed[id] = job{t.M, t.A}
			mu.Unlock()
		}
	}
	mu.Lock()
	persist() // drops Pending
	mu.Unlock()

	offset := 0
	for {
		var updates []struct {
			UpdateID int      `json:"update_id"`
			Message  *message `json:"message"`
		}
		err := api("getUpdates", url.Values{
			"offset": {strconv.Itoa(offset)}, "timeout": {"50"}, "allowed_updates": {`["message"]`},
		}, &updates)
		if err != nil {
			log.Println("poll:", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if len(updates) > 0 {
			log.Printf("poll: %d update(s)", len(updates))
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			m := u.Message
			if m == nil || m.From.ID != owner {
				continue
			}
			log.Printf("recv msg=%d group=%q text=%q hasFile=%v reply=%v", m.MessageID, m.GroupID, m.Text, attachment(m) != nil, m.ReplyTo != nil)
			if m.Text == "/status" {
				reply(m.Chat.ID, statusText())
				continue
			}
			if m.Text == "/retry" {
				target := m.ReplyTo
				if target != nil {
					mu.Lock()
					if o, ok := origin[target.MessageID]; ok {
						target = o // replied to a bot status message
					}
					mu.Unlock()
				}
				retryFailed(m.Chat.ID, target)
				continue
			}
			if a := attachment(m); a != nil {
				enqueue(m, a, fmt.Sprintf("tg-%d", m.MessageID))
			}
		}
	}
}
