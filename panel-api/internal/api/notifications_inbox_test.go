package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// --- fakes ---

type fakeHistoryRepo struct {
	mu       sync.Mutex
	rows     map[string]*models.NotificationHistory
	adminU   string
	markedID string
	markAll  int
}

func (f *fakeHistoryRepo) Create(_ context.Context, h *models.NotificationHistory) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[string]*models.NotificationHistory{}
	}
	dup := *h
	f.rows[h.ID] = &dup
	return nil
}
func (f *fakeHistoryRepo) UpdateOutcome(context.Context, string, string, string, int) error {
	return nil
}
func (f *fakeHistoryRepo) MarkRead(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markedID = id
	if r, ok := f.rows[id]; ok {
		now := time.Now()
		r.ReadAt = &now
	}
	return nil
}
func (f *fakeHistoryRepo) MarkAllReadForUser(_ context.Context, _ string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markAll++
	return 7, nil
}
func (f *fakeHistoryRepo) FindByID(_ context.Context, id string) (*models.NotificationHistory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[id]; ok {
		out := *r
		return &out, nil
	}
	return nil, repository.ErrNotFound
}
func (f *fakeHistoryRepo) ListForUser(_ context.Context, u string, _ repository.ListOptions) ([]models.NotificationHistory, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []models.NotificationHistory
	for _, r := range f.rows {
		if r.UserID != nil && *r.UserID == u {
			out = append(out, *r)
		}
	}
	return out, int64(len(out)), nil
}
func (f *fakeHistoryRepo) ListForAdminInbox(_ context.Context, u string, _ repository.ListOptions) ([]models.NotificationHistory, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []models.NotificationHistory
	for _, r := range f.rows {
		if r.UserID == nil || *r.UserID == u {
			out = append(out, *r)
		}
	}
	return out, int64(len(out)), nil
}
func (f *fakeHistoryRepo) CountUnreadForAdminInbox(_ context.Context, u string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, r := range f.rows {
		if (r.UserID == nil || *r.UserID == u) && r.ReadAt == nil {
			n++
		}
	}
	return n, nil
}
func (f *fakeHistoryRepo) CountUnreadForUser(_ context.Context, u string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, r := range f.rows {
		if r.UserID != nil && *r.UserID == u && r.ReadAt == nil {
			n++
		}
	}
	return n, nil
}
func (f *fakeHistoryRepo) ListRecentByEvent(context.Context, string, time.Time) ([]models.NotificationHistory, error) {
	return nil, nil
}

// --- helpers ---

func newInboxRouter(t *testing.T, repo *fakeHistoryRepo, authFn gin.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v1")
	if authFn != nil {
		g.Use(authFn)
	}
	RegisterNotificationsInboxRoutes(g, NotificationsInboxHandlerConfig{History: repo})
	return r
}

func strPtrForTest(s string) *string { return &s }

// --- tests ---

func TestInbox_User_SeesOnlyOwnRows(t *testing.T) {
	t.Parallel()
	repo := &fakeHistoryRepo{rows: map[string]*models.NotificationHistory{
		"a": {ID: "a", UserID: strPtrForTest("u1"), EventKind: "x", Title: "mine"},
		"b": {ID: "b", UserID: strPtrForTest("u2"), EventKind: "x", Title: "theirs"},
		"c": {ID: "c", UserID: nil, EventKind: "sys", Title: "broadcast"},
	}}
	r := newInboxRouter(t, repo, newUserCtx())
	rec := doNotifJSON(t, r, http.MethodGet, "/api/v1/notifications/inbox", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp inboxListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 1, resp.Total)
	require.Equal(t, "mine", resp.Data[0].Title)
}

func TestInbox_Admin_SeesOwnAndBroadcast(t *testing.T) {
	t.Parallel()
	repo := &fakeHistoryRepo{rows: map[string]*models.NotificationHistory{
		"a": {ID: "a", UserID: strPtrForTest("admin1"), EventKind: "x", Title: "mine"},
		"b": {ID: "b", UserID: strPtrForTest("u2"), EventKind: "x", Title: "other user"},
		"c": {ID: "c", UserID: nil, EventKind: "sys", Title: "broadcast"},
	}}
	r := newInboxRouter(t, repo, newAdminCtx())
	rec := doNotifJSON(t, r, http.MethodGet, "/api/v1/notifications/inbox", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp inboxListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 2, resp.Total)
}

func TestInbox_UnreadOnly_Filters(t *testing.T) {
	t.Parallel()
	read := time.Now()
	repo := &fakeHistoryRepo{rows: map[string]*models.NotificationHistory{
		"a": {ID: "a", UserID: strPtrForTest("u1"), Title: "read", ReadAt: &read},
		"b": {ID: "b", UserID: strPtrForTest("u1"), Title: "unread", ReadAt: nil},
	}}
	r := newInboxRouter(t, repo, newUserCtx())
	rec := doNotifJSON(t, r, http.MethodGet, "/api/v1/notifications/inbox?unread_only=1", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp inboxListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.UnreadOnly)
	require.Len(t, resp.Data, 1)
	require.Equal(t, "unread", resp.Data[0].Title)
}

func TestInbox_MarkRead_OwnRow(t *testing.T) {
	t.Parallel()
	repo := &fakeHistoryRepo{rows: map[string]*models.NotificationHistory{
		"a": {ID: "a", UserID: strPtrForTest("u1")},
	}}
	r := newInboxRouter(t, repo, newUserCtx())
	rec := doNotifJSON(t, r, http.MethodPost, "/api/v1/notifications/inbox/a/read", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "a", repo.markedID)
}

func TestInbox_MarkRead_OtherUserIs403(t *testing.T) {
	t.Parallel()
	repo := &fakeHistoryRepo{rows: map[string]*models.NotificationHistory{
		"a": {ID: "a", UserID: strPtrForTest("someone-else")},
	}}
	r := newInboxRouter(t, repo, newUserCtx())
	rec := doNotifJSON(t, r, http.MethodPost, "/api/v1/notifications/inbox/a/read", nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestInbox_MarkRead_AdminOnBroadcast(t *testing.T) {
	t.Parallel()
	repo := &fakeHistoryRepo{rows: map[string]*models.NotificationHistory{
		"a": {ID: "a", UserID: nil, EventKind: "sys"},
	}}
	r := newInboxRouter(t, repo, newAdminCtx())
	rec := doNotifJSON(t, r, http.MethodPost, "/api/v1/notifications/inbox/a/read", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestInbox_ReadAll(t *testing.T) {
	t.Parallel()
	repo := &fakeHistoryRepo{}
	r := newInboxRouter(t, repo, newUserCtx())
	rec := doNotifJSON(t, r, http.MethodPost, "/api/v1/notifications/inbox/read-all", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, repo.markAll)
	var body map[string]int64
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, int64(7), body["updated"])
}

func TestInbox_Unauthenticated(t *testing.T) {
	t.Parallel()
	repo := &fakeHistoryRepo{}
	r := newInboxRouter(t, repo, nil) // no claims middleware
	rec := doNotifJSON(t, r, http.MethodGet, "/api/v1/notifications/inbox", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
