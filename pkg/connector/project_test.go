package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	testSiteID       = "site-1"
	testProjectID    = "proj-target"
	testMockPageSize = 2
)

// newProjectServer serves auth, empty project permissions and a paginated workbooks listing,
// recording the filter of each workbooks request.
func newProjectServer(t *testing.T, workbooks []*client.Workbook) (*httptest.Server, *[]string) {
	t.Helper()

	var mu sync.Mutex
	var filters []string

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/signin", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"credentials": map[string]any{"token": "token", "site": map[string]string{"id": testSiteID}},
		})
	})
	emptyPermissions := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"permissions": map[string]any{"granteeCapabilities": []any{}}})
	}
	mux.HandleFunc("/sites/"+testSiteID+"/projects/"+testProjectID+"/permissions", emptyPermissions)
	mux.HandleFunc("/sites/"+testSiteID+"/projects/"+testProjectID+"/default-permissions/workbooks", emptyPermissions)
	mux.HandleFunc("/sites/"+testSiteID+"/workbooks", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		filters = append(filters, q.Get("filter"))
		mu.Unlock()

		page, err := strconv.Atoi(q.Get("pageNumber"))
		require.NoError(t, err)
		start := min((page-1)*testMockPageSize, len(workbooks))
		end := min(start+testMockPageSize, len(workbooks))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pagination": map[string]string{
				"pageNumber":     strconv.Itoa(page),
				"pageSize":       strconv.Itoa(testMockPageSize),
				"totalAvailable": strconv.Itoa(len(workbooks)),
			},
			"workbooks": map[string]any{"workbook": workbooks[start:end]},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &filters
}

func newTestProjectBuilder(t *testing.T, ctx context.Context, srv *httptest.Server) *projectBuilder {
	t.Helper()
	c, err := client.New(ctx, "", testSiteID, "name", "secret", "", srv.URL)
	require.NoError(t, err)
	return newProjectBuilder(c)
}

func newBufferedLogCtx(buf *bytes.Buffer) context.Context {
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(buf), zapcore.DebugLevel)
	return ctxzap.ToContext(context.Background(), zap.New(core))
}

func TestProjectGrants_LockedProjectWorkbookPhase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		projectName        string
		contentPermissions string
		wantWorkbookPhase  bool
		wantWarn           bool
	}{
		{name: "locked with plain name", projectName: "Finance", contentPermissions: "LockedToProject", wantWorkbookPhase: true},
		{name: "locked without nested with plain name", projectName: "Finance", contentPermissions: "LockedToProjectWithoutNested", wantWorkbookPhase: true},
		{name: "locked with comma name", projectName: "Core Exp, Nav, and Search", contentPermissions: "LockedToProject", wantWarn: true},
		{name: "locked with ampersand name", projectName: "Sales & Marketing", contentPermissions: "LockedToProject", wantWarn: true},
		{name: "unlocked with comma name", projectName: "Core Exp, Nav, and Search", contentPermissions: "ManagedByOwner"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			ctx := newBufferedLogCtx(&buf)

			srv, filters := newProjectServer(t, nil)
			builder := newTestProjectBuilder(t, ctx, srv)

			project, err := projectResource(&client.Project{ID: testProjectID, Name: tt.projectName, ContentPermissions: tt.contentPermissions})
			require.NoError(t, err)

			_, res, err := builder.Grants(ctx, project, rs.SyncOpAttrs{})
			require.NoError(t, err)

			var b pagination.Bag
			require.NoError(t, b.Unmarshal(res.NextPageToken))
			if tt.wantWorkbookPhase {
				require.Equal(t, resourceTypeWorkbook.Id, b.ResourceTypeID())
			} else {
				require.Empty(t, res.NextPageToken)
			}
			require.Empty(t, *filters, "phase 1 must not list workbooks")

			logs := buf.String()
			if tt.wantWarn {
				require.Contains(t, logs, `"level":"warn"`)
				require.Contains(t, logs, `"project_id":"`+testProjectID+`"`)
			} else {
				require.NotContains(t, logs, "skipping inherited workbook grants")
			}
		})
	}
}

func TestProjectGrants_WorkbookPhaseFiltersByProjectName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	workbookIn := func(id, projectID string) *client.Workbook {
		return &client.Workbook{ID: id, Name: id, Project: &client.ProjectRef{ID: projectID, Name: "Finance"}}
	}
	srv, filters := newProjectServer(t, []*client.Workbook{
		workbookIn("wb-1", testProjectID),
		workbookIn("wb-2", "proj-same-name"),
		workbookIn("wb-3", testProjectID),
	})
	builder := newTestProjectBuilder(t, ctx, srv)

	project, err := projectResource(&client.Project{ID: testProjectID, Name: "Finance", ContentPermissions: "LockedToProject"})
	require.NoError(t, err)

	var b pagination.Bag
	b.Push(pagination.PageState{ResourceTypeID: resourceTypeWorkbook.Id})
	token, err := b.Marshal()
	require.NoError(t, err)

	granted := map[string]bool{}
	seenTokens := map[string]bool{}
	for token != "" {
		require.False(t, seenTokens[token], "page token repeated: %s", token)
		seenTokens[token] = true

		grants, res, err := builder.Grants(ctx, project, rs.SyncOpAttrs{PageToken: pagination.Token{Token: token}})
		require.NoError(t, err)
		for _, g := range grants {
			require.Equal(t, resourceTypeProject.Id, g.Principal.Id.ResourceType)
			require.Equal(t, testProjectID, g.Principal.Id.Resource)
			granted[g.Entitlement.Resource.Id.Resource] = true
		}
		token = res.NextPageToken
	}

	require.Len(t, *filters, 2)
	for _, f := range *filters {
		require.Equal(t, "projectName:eq:Finance", f)
	}
	require.Equal(t, map[string]bool{"wb-1": true, "wb-3": true}, granted)
}

func TestSampledWarn_LogsOnlyOnSampledOccurrences(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	ctx := newBufferedLogCtx(&buf)

	var s sampledWarn
	for range 12 {
		s.log(ctx, "skipped project")
	}

	logs := buf.String()
	require.Equal(t, 2, strings.Count(logs, "skipped project"), "expected occurrences 1 and 10 out of 12")
	require.Contains(t, logs, `"total_occurrences":1}`)
	require.Contains(t, logs, `"total_occurrences":10}`)
	require.Equal(t, 2, strings.Count(logs, `"level":"warn"`))
}

func TestShouldLogSample(t *testing.T) {
	t.Parallel()
	for n, want := range map[uint64]bool{0: true, 1: true, 2: false, 10: true, 11: false, 100: true, 999: false, 1000: true, 1001: false, 2000: true} {
		require.Equal(t, want, shouldLogSample(n), "n=%d", n)
	}
}
