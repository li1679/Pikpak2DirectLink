package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"pikpak2directlink/internal/pikpak"
)

type AccountStatus string

const (
	AccountAvailable AccountStatus = "available"
	AccountFailed    AccountStatus = "failed"
)

// bytesPerGB is the binary gibibyte (1024³), matching PikPak file sizes.
const bytesPerGB = int64(1) << 30

// defaultAccountTraffic is the monthly downstream-traffic budget: 700 GiB.
const defaultAccountTraffic = 700 * bytesPerGB

type AccountPoolConfig struct {
	RootFolderName string
	RequestTimeout time.Duration
	Store          *accountStore
}

type AccountSummary struct {
	ID                    string        `json:"id"`
	Username              string        `json:"username"`
	Status                AccountStatus `json:"status"`
	Ready                 bool          `json:"ready"`
	LoggedIn              bool          `json:"logged_in"`
	Persisted             bool          `json:"persisted"`
	Premium               bool          `json:"premium"`
	PremiumType           string        `json:"premium_type,omitempty"`
	PremiumUntil          string        `json:"premium_until,omitempty"`
	PremiumError          string        `json:"premium_error,omitempty"`
	PremiumCheckedAt      string        `json:"premium_checked_at,omitempty"`
	TrafficLimit          int64         `json:"traffic_limit"`
	TrafficUsed           int64         `json:"traffic_used"`
	TrafficLimited        bool          `json:"traffic_limited"`
	LastError             string        `json:"last_error,omitempty"`
	LastFailedAt          string        `json:"last_failed_at,omitempty"`
	CredentialCheckedAt   string        `json:"credential_checked_at,omitempty"`
	CredentialNextCheckAt string        `json:"credential_next_check_at,omitempty"`
	CredentialCheckError  string        `json:"credential_check_error,omitempty"`
	ParseErrorCount       int           `json:"parse_error_count"`
	ParseErrors           []ParseError  `json:"parse_errors,omitempty"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
}

type AccountRuntime struct {
	ID       string
	Username string
	Client   pikpakClient
}

type pikpakClient interface {
	Login(ctx context.Context, username, password string) error
	Status() pikpak.SessionStatus
	EnsureRootFolder(ctx context.Context) (string, error)
	CreateFolder(ctx context.Context, name, parentID string) (*pikpak.FileEntry, error)
	CreateOfflineTask(ctx context.Context, sourceURL, parentID, name string) (*pikpak.TaskEntry, error)
	ListOfflineTasks(ctx context.Context, phases []string) ([]pikpak.TaskEntry, error)
	ListFiles(ctx context.Context, parentID string) ([]pikpak.FileEntry, error)
	GetFile(ctx context.Context, fileID string) (*pikpak.FileEntry, error)
	GetVIPInfo(ctx context.Context) (*pikpak.VIPInfo, error)
	GetShareInfo(ctx context.Context, shareID, passCode, parentID string) (*pikpak.ShareListResponse, error)
	GetShareFolder(ctx context.Context, shareID, passCodeToken, parentID string) (*pikpak.ShareListResponse, error)
	RestoreShare(ctx context.Context, shareID, passCodeToken string, fileIDs []string) (*pikpak.RestoreShareResponse, error)
	WaitForFileDownloadURL(ctx context.Context, fileID string, timeout, pollInterval time.Duration) (*pikpak.FileEntry, error)
	DeleteFiles(ctx context.Context, fileIDs []string) error
}

type accountRecord struct {
	ID                    string        `json:"id"`
	Username              string        `json:"username"`
	Password              string        `json:"password"`
	Status                AccountStatus `json:"status"`
	Premium               bool          `json:"premium"`
	PremiumType           string        `json:"premium_type,omitempty"`
	PremiumUntil          string        `json:"premium_until,omitempty"`
	PremiumError          string        `json:"premium_error,omitempty"`
	PremiumCheckedAt      string        `json:"premium_checked_at,omitempty"`
	TrafficLimit          int64         `json:"traffic_limit,omitempty"`
	TrafficUsed           int64         `json:"traffic_used,omitempty"`
	TrafficPeriod         string        `json:"traffic_period,omitempty"`
	LastError             string        `json:"last_error,omitempty"`
	LastFailedAt          string        `json:"last_failed_at,omitempty"`
	CredentialCheckedAt   string        `json:"credential_checked_at,omitempty"`
	CredentialNextCheckAt string        `json:"credential_next_check_at,omitempty"`
	CredentialCheckError  string        `json:"credential_check_error,omitempty"`
	ParseErrors           []ParseError  `json:"parse_errors,omitempty"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
}

type ParseError struct {
	Time    string `json:"time"`
	JobID   string `json:"job_id,omitempty"`
	Message string `json:"message"`
}

type accountState struct {
	record accountRecord
	client pikpakClient
}

type accountRecordReplacer interface {
	Replace([]accountRecord) error
}

type AccountPool struct {
	mu             sync.RWMutex
	config         AccountPoolConfig
	accounts       map[string]*accountState
	order          []string
	cursor         uint64 // rotating round-robin starting point for parallel resolves
	store          *accountStore
	recordReplacer accountRecordReplacer
}

type accountPoolSnapshot struct {
	accounts map[string]*accountState
	order    []string
}

const (
	premiumRefreshInterval    = 30 * time.Minute
	premiumRefreshConcurrency = 4
)

const badResourceParseUserError = "该磁链连续遇到解析错误，请不要反复重试此链接。"

var errAccountCredentialsRequired = errors.New("请填写用户名和密码")

func NewAccountPool(cfg AccountPoolConfig) (*AccountPool, error) {
	if cfg.Store == nil {
		return nil, errors.New("SQLite account storage is required")
	}
	if err := cfg.Store.validate(); err != nil {
		return nil, err
	}

	pool := &AccountPool{
		config:   cfg,
		accounts: make(map[string]*accountState),
		store:    cfg.Store,
	}
	pool.recordReplacer = cfg.Store
	if err := pool.load(); err != nil {
		return nil, err
	}
	return pool, nil
}

// Add saves credentials locally without requiring a successful remote login.
// Network verification belongs to first use or an explicit RefreshLogin call.
func (p *AccountPool) Add(_ context.Context, username, password string, trafficLimit int64) (AccountSummary, error) {
	username = strings.TrimSpace(username)
	if username == "" || strings.TrimSpace(password) == "" {
		return AccountSummary{}, errAccountCredentialsRequired
	}
	if trafficLimit <= 0 {
		trafficLimit = defaultAccountTraffic
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	id := p.accountIdentityLocked(username)
	now := time.Now()
	existing, existed := p.accounts[id]
	record := accountRecord{ID: id, Username: username, Password: password, Status: AccountAvailable,
		TrafficLimit: trafficLimit, TrafficPeriod: monthKey(now), CreatedAt: now, UpdatedAt: now,
		CredentialNextCheckAt: formatAccountTime(now.Add(defaultAccountHealthCheckInterval))}
	resetSession := !existed || existing.record.Password != password
	if existed {
		record.CreatedAt = existing.record.CreatedAt
		record.TrafficUsed = existing.record.TrafficUsed
		record.ParseErrors = append([]ParseError(nil), existing.record.ParseErrors...)
		if existing.record.TrafficPeriod != "" {
			record.TrafficPeriod = existing.record.TrafficPeriod
		}
		if !resetSession {
			record.Premium, record.PremiumType, record.PremiumUntil = existing.record.Premium, existing.record.PremiumType, existing.record.PremiumUntil
			record.PremiumCheckedAt = existing.record.PremiumCheckedAt
			record.CredentialCheckedAt = existing.record.CredentialCheckedAt
		}
	}
	if err := p.store.SaveCredentials(record, resetSession); err != nil {
		return AccountSummary{}, err
	}
	var client pikpakClient = p.newClient(id, username, password)
	if existed && !resetSession {
		client = existing.client
	}
	p.accounts[id] = &accountState{record: record, client: client}
	if !existed {
		p.order = append(p.order, id)
	}
	return p.summaryLocked(id), nil
}

func (p *AccountPool) AddBootstrap(username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" || strings.TrimSpace(password) == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	id := p.accountIdentityLocked(username)
	if _, ok := p.accounts[id]; ok {
		return nil
	}
	now := time.Now()
	record := accountRecord{
		ID: id, Username: username, Password: password, Status: AccountAvailable,
		TrafficLimit: defaultAccountTraffic, TrafficPeriod: monthKey(now),
		CredentialNextCheckAt: formatAccountTime(now), CreatedAt: now, UpdatedAt: now,
	}
	if err := p.store.Insert(record); err != nil {
		return err
	}
	p.accounts[id] = &accountState{record: record, client: p.newClient(id, username, password)}
	p.order = append(p.order, id)
	return nil
}

func (p *AccountPool) List() []AccountSummary {
	p.mu.RLock()
	defer p.mu.RUnlock()

	summaries := make([]AccountSummary, 0, len(p.order))
	for _, id := range p.order {
		summaries = append(summaries, p.summaryLocked(id))
	}
	return summaries
}

func (p *AccountPool) Summary(id string) (AccountSummary, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.accounts[id] == nil {
		return AccountSummary{}, false
	}
	return p.summaryLocked(id), true
}

func (p *AccountPool) EnsureCredentialSchedule(now time.Time, interval time.Duration) error {
	if interval <= 0 {
		interval = defaultAccountHealthCheckInterval
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := p.snapshotLocked()

	changed := false
	next := formatAccountTime(now.Add(interval))
	for _, id := range p.order {
		state := p.accounts[id]
		if state == nil {
			continue
		}
		if _, err := parseAccountTime(state.record.CredentialNextCheckAt); state.record.CredentialNextCheckAt == "" || err != nil {
			state.record.CredentialNextCheckAt = next
			state.record.UpdatedAt = now
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return p.saveOrRollbackLocked(snapshot)
}

type credentialCheckTarget struct {
	Account AccountRuntime
	NextAt  time.Time
}

func (p *AccountPool) NextCredentialCheck(now time.Time) (credentialCheckTarget, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var target credentialCheckTarget
	for _, id := range p.order {
		state := p.accounts[id]
		if state == nil {
			continue
		}
		nextAt, err := parseAccountTime(state.record.CredentialNextCheckAt)
		if err != nil {
			nextAt = now
		}
		if target.Account.ID != "" && !nextAt.Before(target.NextAt) {
			continue
		}
		target = credentialCheckTarget{
			Account: AccountRuntime{
				ID:       state.record.ID,
				Username: state.record.Username,
				Client:   state.client,
			},
			NextAt: nextAt,
		}
	}
	return target, target.Account.ID != ""
}

func (p *AccountPool) RefreshPremiumInfo(ctx context.Context) error {
	now := time.Now()

	type target struct {
		id     string
		state  *accountState
		client pikpakClient
	}
	type result struct {
		target    target
		info      *pikpak.VIPInfo
		err       error
		checkedAt time.Time
	}

	p.mu.RLock()
	targets := make([]target, 0, len(p.order))
	for _, id := range p.order {
		state := p.accounts[id]
		if state == nil || state.client == nil || !state.client.Status().LoggedIn {
			continue
		}
		if premiumInfoNeedsRefresh(state.record, now) {
			targets = append(targets, target{
				id:     id,
				state:  state,
				client: state.client,
			})
		}
	}
	p.mu.RUnlock()
	if len(targets) == 0 {
		return nil
	}

	jobs := make(chan target, len(targets))
	results := make(chan result, len(targets))
	for _, item := range targets {
		jobs <- item
	}
	close(jobs)

	workerCount := min(len(targets), premiumRefreshConcurrency)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				info, err := item.client.GetVIPInfo(ctx)
				results <- result{target: item, info: info, err: err, checkedAt: time.Now()}
			}
		}()
	}
	workers.Wait()
	close(results)

	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := p.snapshotLocked()
	changed := false
	for item := range results {
		state := p.accounts[item.target.id]
		if state == nil || state != item.target.state || !samePikPakClient(state.client, item.target.client) {
			continue
		}
		updatePremiumRecord(&state.record, item.info, item.err, item.checkedAt)
		state.record.UpdatedAt = item.checkedAt
		changed = true
	}
	if !changed {
		return nil
	}
	return p.saveOrRollbackLocked(snapshot)
}

func samePikPakClient(current, snapshot pikpakClient) bool {
	currentValue := reflect.ValueOf(current)
	snapshotValue := reflect.ValueOf(snapshot)
	if !currentValue.IsValid() || !snapshotValue.IsValid() {
		return !currentValue.IsValid() && !snapshotValue.IsValid()
	}
	if currentValue.Type() != snapshotValue.Type() || !currentValue.Comparable() {
		return false
	}
	return currentValue.Interface() == snapshotValue.Interface()
}

func (p *AccountPool) MarkCredentialCheckSuccess(id string, checkedAt, nextAt time.Time, cleanupErr error) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return nil
	}
	snapshot := p.snapshotLocked()
	state.record.Status = AccountAvailable
	state.record.LastError = ""
	state.record.LastFailedAt = ""
	state.record.CredentialCheckedAt = formatAccountTime(checkedAt)
	state.record.CredentialNextCheckAt = formatAccountTime(nextAt)
	state.record.CredentialCheckError = ""
	if cleanupErr != nil {
		state.record.CredentialCheckError = "测试文件清理失败：" + friendlyPikPakError(cleanupErr)
	}
	state.record.UpdatedAt = checkedAt
	return p.saveOrRollbackLocked(snapshot)
}

func (p *AccountPool) MarkCredentialCheckFailed(id string, err error, checkedAt, nextAt time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return nil
	}
	snapshot := p.snapshotLocked()
	message := friendlyPikPakError(err)
	if message == "" {
		message = "账号凭据验证失败"
	}
	state.record.Status = AccountFailed
	state.record.LastError = message
	state.record.LastFailedAt = checkedAt.Format(time.RFC3339)
	state.record.CredentialCheckedAt = formatAccountTime(checkedAt)
	state.record.CredentialNextCheckAt = formatAccountTime(nextAt)
	state.record.CredentialCheckError = message
	state.record.UpdatedAt = checkedAt
	return p.saveOrRollbackLocked(snapshot)
}

func (p *AccountPool) Snapshot() []AccountRuntime {
	p.mu.RLock()
	defer p.mu.RUnlock()

	accounts := make([]AccountRuntime, 0, len(p.order))
	for _, id := range p.order {
		state := p.accounts[id]
		if state == nil {
			continue
		}
		accounts = append(accounts, AccountRuntime{
			ID:       state.record.ID,
			Username: state.record.Username,
			Client:   state.client,
		})
	}
	return accounts
}

// ResolveOrder returns every account below its own traffic limit. A previous
// validation failure is only a hint; each automatic task still retries it.
// Parallel starts rotate across the complete pool, including failed accounts.
func (p *AccountPool) ResolveOrder(rotate bool) []AccountRuntime {
	now := time.Now()
	p.mu.RLock()
	accounts := make([]AccountRuntime, 0, len(p.order))
	for _, id := range p.order {
		state := p.accounts[id]
		if state == nil || accountTrafficLimited(state.record, now) {
			continue
		}
		accounts = append(accounts, AccountRuntime{ID: state.record.ID, Username: state.record.Username, Client: state.client})
	}
	p.mu.RUnlock()
	if rotate && len(accounts) > 1 {
		offset := int((atomic.AddUint64(&p.cursor, 1) - 1) % uint64(len(accounts)))
		rotated := make([]AccountRuntime, 0, len(accounts))
		rotated = append(rotated, accounts[offset:]...)
		rotated = append(rotated, accounts[:offset]...)
		accounts = rotated
	}
	return accounts
}

func (p *AccountPool) Get(id string) (AccountRuntime, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	state := p.accounts[id]
	if state == nil {
		return AccountRuntime{}, false
	}
	return AccountRuntime{
		ID:       state.record.ID,
		Username: state.record.Username,
		Client:   state.client,
	}, true
}

func (p *AccountPool) HasAccounts() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.order) > 0
}

func (p *AccountPool) Delete(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return errors.New("account not found")
	}
	snapshot := p.snapshotLocked()

	delete(p.accounts, id)
	for i, accountID := range p.order {
		if accountID == id {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}

	if err := p.saveOrRollbackLocked(snapshot); err != nil {
		return err
	}
	return nil
}

func (p *AccountPool) ResetFailure(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return errors.New("account not found")
	}
	snapshot := p.snapshotLocked()
	state.record.Status = AccountAvailable
	state.record.LastError = ""
	state.record.LastFailedAt = ""
	state.record.UpdatedAt = time.Now()
	return p.saveOrRollbackLocked(snapshot)
}

func (p *AccountPool) RefreshLogin(ctx context.Context, id string) (AccountSummary, error) {
	p.mu.RLock()
	state := p.accounts[id]
	if state == nil {
		p.mu.RUnlock()
		return AccountSummary{}, errors.New("account not found")
	}
	record := state.record
	p.mu.RUnlock()

	if strings.TrimSpace(record.Username) == "" || strings.TrimSpace(record.Password) == "" {
		return AccountSummary{}, errors.New("account username or password is missing")
	}

	staged := newStagingSessionStore()
	client := p.newClientWithSessionStore(record.Username, record.Password, staged)
	if err := client.Login(ctx, record.Username, record.Password); err != nil {
		return AccountSummary{}, err
	}
	premiumInfo, premiumErr := client.GetVIPInfo(ctx)

	p.mu.Lock()
	defer p.mu.Unlock()

	state = p.accounts[id]
	if state == nil {
		return AccountSummary{}, errors.New("account not found")
	}
	if state.record.Username != record.Username || state.record.Password != record.Password {
		return AccountSummary{}, errors.New("account credentials changed during login; retry")
	}
	session, err := staged.Load()
	if err != nil {
		return AccountSummary{}, fmt.Errorf("read staged PikPak session: %w", err)
	}
	now := time.Now()
	updated := state.record
	updated.Status = AccountAvailable
	updated.LastError = ""
	updated.LastFailedAt = ""
	updatePremiumRecord(&updated, premiumInfo, premiumErr, now)
	updated.UpdatedAt = now
	if err := p.store.UpsertWithSession(updated, session); err != nil {
		return AccountSummary{}, err
	}
	state.record = updated
	state.client = p.newClient(id, updated.Username, updated.Password)
	return p.summaryLocked(id), nil
}

func (p *AccountPool) MarkFailed(id string, err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return nil
	}
	snapshot := p.snapshotLocked()
	state.record.Status = AccountFailed
	state.record.LastError = friendlyPikPakError(err)
	state.record.LastFailedAt = time.Now().Format(time.RFC3339)
	state.record.UpdatedAt = time.Now()
	return p.saveOrRollbackLocked(snapshot)
}

func (p *AccountPool) MarkAvailable(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return nil
	}
	snapshot := p.snapshotLocked()
	state.record.Status = AccountAvailable
	state.record.LastError = ""
	state.record.LastFailedAt = ""
	state.record.UpdatedAt = time.Now()
	return p.saveOrRollbackLocked(snapshot)
}

func (p *AccountPool) RecordParseError(id, jobID, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return
	}
	snapshot := p.snapshotLocked()
	message = strings.TrimSpace(message)
	if message == "" {
		message = "record not found"
	}
	now := time.Now()
	state.record.ParseErrors = append(state.record.ParseErrors, ParseError{
		Time:    now.Format(time.RFC3339),
		JobID:   strings.TrimSpace(jobID),
		Message: message,
	})
	state.record.UpdatedAt = now
	_ = p.saveOrRollbackLocked(snapshot)
}

func (p *AccountPool) DeleteParseError(id string, index int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return errors.New("account not found")
	}
	if index < 0 || index >= len(state.record.ParseErrors) {
		return errors.New("parse error not found")
	}
	snapshot := p.snapshotLocked()
	state.record.ParseErrors = append(state.record.ParseErrors[:index], state.record.ParseErrors[index+1:]...)
	state.record.UpdatedAt = time.Now()
	return p.saveOrRollbackLocked(snapshot)
}

// SetTrafficLimit updates an account's monthly downstream budget (in bytes).
func (p *AccountPool) SetTrafficLimit(id string, limitBytes int64) error {
	if limitBytes <= 0 {
		limitBytes = defaultAccountTraffic
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return errors.New("account not found")
	}
	snapshot := p.snapshotLocked()
	state.record.TrafficLimit = limitBytes
	state.record.UpdatedAt = time.Now()
	return p.saveOrRollbackLocked(snapshot)
}

// AddTraffic records bytes of downstream traffic against an account for the
// current month. The counter rolls over automatically when the calendar month
// changes, which is how the monthly "到达限行流量" state clears itself.
func (p *AccountPool) AddTraffic(id string, bytes int64) {
	if bytes <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.accounts[id]
	if state == nil {
		return
	}
	snapshot := p.snapshotLocked()
	now := time.Now()
	mk := monthKey(now)
	if state.record.TrafficPeriod != mk {
		state.record.TrafficUsed = 0
		state.record.TrafficPeriod = mk
	}
	state.record.TrafficUsed += bytes
	state.record.UpdatedAt = now
	_ = p.saveOrRollbackLocked(snapshot)
}

type accountTrafficUpdate struct {
	pool    *AccountPool
	id      string
	record  accountRecord
	changed bool
	closed  bool
}

func (p *AccountPool) beginTrafficUpdate(id string, bytes int64, now time.Time) (*accountTrafficUpdate, error) {
	if p == nil || p.store == nil {
		return nil, errors.New("durable account storage is not configured")
	}
	p.mu.Lock()
	state := p.accounts[id]
	if state == nil {
		p.mu.Unlock()
		return nil, errors.New("account not found")
	}
	record := state.record
	record.ParseErrors = append([]ParseError(nil), state.record.ParseErrors...)
	update := &accountTrafficUpdate{pool: p, id: id, record: record, changed: bytes > 0}
	if bytes > 0 {
		period := monthKey(now)
		if update.record.TrafficPeriod != period {
			update.record.TrafficUsed = 0
			update.record.TrafficPeriod = period
		}
		update.record.TrafficUsed += bytes
		update.record.UpdatedAt = now
	}
	return update, nil
}

func (u *accountTrafficUpdate) writeTx(tx *sql.Tx) error {
	if u == nil || !u.changed {
		return nil
	}
	return u.pool.store.updateTx(tx, u.record)
}

func (u *accountTrafficUpdate) finish(committed bool) {
	if u == nil || u.closed {
		return
	}
	if committed && u.changed {
		if state := u.pool.accounts[u.id]; state != nil {
			state.record = u.record
		}
	}
	u.closed = true
	u.pool.mu.Unlock()
}

func (p *AccountPool) load() error {
	records, err := p.store.List()
	if err != nil {
		return err
	}
	for _, record := range records {
		p.accounts[record.ID] = &accountState{
			record: record,
			client: p.newClient(record.ID, record.Username, record.Password),
		}
		p.order = append(p.order, record.ID)
	}
	return nil
}

func (p *AccountPool) saveLocked() error {
	records := make([]accountRecord, 0, len(p.order))
	for _, id := range p.order {
		state := p.accounts[id]
		if state == nil {
			continue
		}
		records = append(records, state.record)
	}
	if p.recordReplacer != nil {
		return p.recordReplacer.Replace(records)
	}
	return errors.New("account persistence is not initialized")
}

func (p *AccountPool) snapshotLocked() accountPoolSnapshot {
	snapshot := accountPoolSnapshot{
		accounts: make(map[string]*accountState, len(p.accounts)),
		order:    append([]string(nil), p.order...),
	}
	for id, state := range p.accounts {
		if state == nil {
			snapshot.accounts[id] = nil
			continue
		}
		stateCopy := *state
		stateCopy.record.ParseErrors = append([]ParseError(nil), state.record.ParseErrors...)
		snapshot.accounts[id] = &stateCopy
	}
	return snapshot
}

func (p *AccountPool) saveOrRollbackLocked(snapshot accountPoolSnapshot) error {
	if err := p.saveLocked(); err != nil {
		p.accounts = snapshot.accounts
		p.order = snapshot.order
		return err
	}
	return nil
}

func (p *AccountPool) summaryLocked(id string) AccountSummary {
	state := p.accounts[id]
	if state == nil {
		return AccountSummary{}
	}

	status := state.client.Status()
	now := time.Now()
	return AccountSummary{
		ID:                    state.record.ID,
		Username:              state.record.Username,
		Status:                state.record.Status,
		Ready:                 status.Ready,
		LoggedIn:              status.LoggedIn,
		Persisted:             status.Persisted,
		Premium:               state.record.Premium,
		PremiumType:           state.record.PremiumType,
		PremiumUntil:          state.record.PremiumUntil,
		PremiumError:          friendlyPikPakMessage(state.record.PremiumError),
		PremiumCheckedAt:      state.record.PremiumCheckedAt,
		TrafficLimit:          state.record.TrafficLimit,
		TrafficUsed:           effectiveTrafficUsed(state.record, now),
		TrafficLimited:        accountTrafficLimited(state.record, now),
		LastError:             friendlyPikPakMessage(state.record.LastError),
		LastFailedAt:          state.record.LastFailedAt,
		CredentialCheckedAt:   state.record.CredentialCheckedAt,
		CredentialNextCheckAt: state.record.CredentialNextCheckAt,
		CredentialCheckError:  friendlyPikPakMessage(state.record.CredentialCheckError),
		ParseErrorCount:       len(state.record.ParseErrors),
		ParseErrors:           append([]ParseError(nil), state.record.ParseErrors...),
		CreatedAt:             state.record.CreatedAt,
		UpdatedAt:             state.record.UpdatedAt,
	}
}

func (p *AccountPool) newClient(id, username, password string) *pikpak.Client {
	return p.newClientWithSessionStore(username, password, p.store.SessionStore(id))
}

func (p *AccountPool) newClientWithSessionStore(username, password string, store pikpak.SessionStore) *pikpak.Client {
	return pikpak.NewClient(pikpak.Config{
		Username:       username,
		Password:       password,
		SessionStore:   store,
		RootFolderName: p.config.RootFolderName,
		RequestTimeout: p.config.RequestTimeout,
	})
}

// accountIdentityLocked must be called with p.mu held.
func (p *AccountPool) accountIdentityLocked(username string) string {
	normalized := strings.ToLower(strings.TrimSpace(username))
	for existingID, state := range p.accounts {
		if state != nil && strings.ToLower(strings.TrimSpace(state.record.Username)) == normalized {
			return existingID
		}
	}
	return "acct_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// monthKey is the calendar-month identifier used to scope traffic counters.
func monthKey(t time.Time) string {
	return t.Format("2006-01")
}

func parseAccountTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("time is empty")
	}
	return time.Parse(time.RFC3339, value)
}

func formatAccountTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// effectiveTrafficUsed returns the bytes used in the month that contains now.
// A counter stamped with an earlier month is treated as already reset to 0,
// which is what makes the monthly refresh automatic (no scheduler needed).
func effectiveTrafficUsed(rec accountRecord, now time.Time) int64 {
	if rec.TrafficPeriod != monthKey(now) {
		return 0
	}
	if rec.TrafficUsed < 0 {
		return 0
	}
	return rec.TrafficUsed
}

// accountTrafficLimited reports whether an account has spent its monthly budget.
// A non-positive limit is treated as unlimited.
func accountTrafficLimited(rec accountRecord, now time.Time) bool {
	if rec.TrafficLimit <= 0 {
		return false
	}
	return effectiveTrafficUsed(rec, now) >= rec.TrafficLimit
}

func premiumInfoNeedsRefresh(record accountRecord, now time.Time) bool {
	if record.PremiumCheckedAt == "" {
		return true
	}

	checkedAt, err := time.Parse(time.RFC3339, record.PremiumCheckedAt)
	if err != nil {
		return true
	}
	return now.Sub(checkedAt) >= premiumRefreshInterval
}

func updatePremiumRecord(record *accountRecord, info *pikpak.VIPInfo, err error, checkedAt time.Time) {
	record.PremiumCheckedAt = checkedAt.Format(time.RFC3339)
	if err != nil {
		record.PremiumError = friendlyPikPakError(err)
		return
	}
	if info == nil {
		record.PremiumError = "empty premium response"
		return
	}

	record.Premium = info.IsPremium()
	record.PremiumType = strings.TrimSpace(info.Data.Type)
	record.PremiumUntil = strings.TrimSpace(info.Expiration())
	record.PremiumError = ""
}

func friendlyPikPakError(err error) string {
	if err == nil {
		return ""
	}
	return friendlyPikPakMessage(err.Error())
}

func friendlyPikPakMessage(message string) string {
	message = strings.TrimSpace(message)
	lower := strings.ToLower(message)
	if strings.Contains(lower, "result:review") ||
		strings.Contains(lower, `value:"review"`) ||
		strings.Contains(lower, "value:\"review\"") {
		return "PikPak 触发登录风控，请先在官方客户端完成验证后再重试。"
	}
	if isResourceUnavailableMessage(lower) {
		return "该资源涉及版权或违规内容，或已失效，已被 PikPak 下架，无法解析。"
	}
	return message
}

// isResourceUnavailableError reports whether an error is a deterministic
// resource-level refusal (the magnet/share was taken down for copyright or
// harmful content, or otherwise no longer exists). Such an error is NOT the
// account's fault — every account would hit the same refusal — so the caller
// must not blacklist the account over it.
func isResourceUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	return isResourceUnavailableMessage(strings.ToLower(err.Error()))
}

func isResourceUnavailableMessage(lower string) bool {
	return strings.Contains(lower, "copyright") ||
		strings.Contains(lower, "harmful content") ||
		strings.Contains(lower, "no longer available")
}

// isResourceParseError reports whether PikPak rejected this specific resource
// while the account/session itself should stay healthy.
func isResourceParseError(err error) bool {
	if err == nil {
		return false
	}
	return isResourceParseMessage(strings.ToLower(err.Error()))
}

func isResourceParseMessage(lower string) bool {
	return strings.Contains(lower, "record not found") ||
		(strings.Contains(lower, "selected share file") && strings.Contains(lower, "could not be found after restore"))
}

func isBadResourceUserError(message string) bool {
	return strings.TrimSpace(message) == badResourceParseUserError
}
