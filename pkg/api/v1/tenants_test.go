package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.infratographer.com/tenant-api/internal/pubsub"
	"go.infratographer.com/x/echojwtx"
	"go.infratographer.com/x/gidx"
	"go.infratographer.com/x/pubsubx"
)

const (
	natsMsgSubTimeout   = 2 * time.Second
	tenantSubjectCreate = "com.infratographer.events.tenants.create.global"
	tenantSubjectUpdate = "com.infratographer.events.tenants.update.global"
	tenantSubjectDelete = "com.infratographer.events.tenants.delete.global"
)

func TestTenantsWithoutAuth(t *testing.T) {
	srv, err := newTestServer(t, nil)
	defer srv.close()

	require.NoError(t, err, "no error expected for new test server")

	t.Run("no tenants", func(t *testing.T) {
		var result *v1TenantSliceResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants", nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.NotNil(t, result, "expected tenants result")
		assert.Equal(t, apiVersion, result.Version, "unexpected response version")
		assert.Len(t, result.Tenants, 0, "expected no tenants")
	})

	subscriber := newPubSubClient(t, srv.logger, srv.nats.ClientURL())
	msgChan := make(chan *nats.Msg, 10)

	// create a new nats subscription on the server created above
	subscription, err := subscriber.ChanSubscribe(
		context.TODO(),
		"com.infratographer.events.tenants.>",
		msgChan,
		"tenant-api-test",
	)

	require.NoError(t, err)

	defer func() {
		if err := subscription.Unsubscribe(); err != nil {
			t.Error(err)
		}
	}()

	var t1Resp *v1TenantResponse

	t.Run("new tenant", func(t *testing.T) {
		createRequest := strings.NewReader(`{"name": "tenant1"}`)

		resp, err := srv.Request(http.MethodPost, "/v1/tenants", nil, createRequest, &t1Resp)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for creating tenant")
		assert.Equal(t, http.StatusCreated, resp.StatusCode, "unexpected status code returned")
		assert.NotEmpty(t, t1Resp.Tenant.ID, "expected tenant id")
		assert.Equal(t, "tenant1", t1Resp.Tenant.Name, "unexpected tenant name")

		select {
		case msg := <-msgChan:
			pMsg := &pubsubx.ChangeMessage{}
			err = json.Unmarshal(msg.Data, pMsg)
			require.NoError(t, err)

			assert.Equal(t, tenantSubjectCreate, msg.Subject, "expected nats subject to be tenant create subject")
			assert.Empty(t, pMsg.ActorID, "expected no actor for unauthenticated client")
			assert.Equal(t, pubsub.CreateEventType, pMsg.EventType, "expected event type to be create")
			assert.Equal(t, t1Resp.Tenant.ID, pMsg.SubjectID, "expected subject id to be returned tenant id")
		case <-time.After(natsMsgSubTimeout):
			t.Error("failed to receive nats message")
		}
	})

	var t1aResp *v1TenantResponse

	t.Run("new subtenant", func(t *testing.T) {
		createRequest := strings.NewReader(`{"name": "tenant1.a"}`)

		resp, err := srv.Request(http.MethodPost, "/v1/tenants/"+string(t1Resp.Tenant.ID)+"/tenants", nil, createRequest, &t1aResp)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for creating subtenant")
		assert.Equal(t, http.StatusCreated, resp.StatusCode, "unexpected status code returned")
		assert.NotEmpty(t, t1aResp.Tenant.ID, "expected tenant id")
		assert.Equal(t, "tenant1.a", t1aResp.Tenant.Name, "unexpected tenant name")
		require.NotNil(t, t1aResp.Tenant.ParentTenantID, "expected parent tenant id to be set")
		assert.Equal(t, t1Resp.Tenant.ID, *t1aResp.Tenant.ParentTenantID, "unexpected parent tenant id")

		select {
		case msg := <-msgChan:
			pMsg := &pubsubx.ChangeMessage{}
			err = json.Unmarshal(msg.Data, pMsg)
			assert.NoError(t, err)

			assert.Equal(t, tenantSubjectCreate, msg.Subject, "expected nats subject to be tenant create subject")
			assert.Empty(t, pMsg.ActorID, "expected no actor for unauthenticated client")
			assert.Equal(t, pubsub.CreateEventType, pMsg.EventType, "expected event type to be create")
			assert.Equal(t, t1aResp.Tenant.ID, pMsg.SubjectID, "expected subject id to be returned tenant id")
			require.NotEmpty(t, pMsg.AdditionalSubjectIDs, "expected additional subject ids")
			assert.Contains(t, pMsg.AdditionalSubjectIDs, t1Resp.Tenant.ID, "expected parent id in additional subject ids")
		case <-time.After(natsMsgSubTimeout):
			t.Error("failed to receive nats message")
		}
	})

	t.Run("list tenants", func(t *testing.T) {
		var result *v1TenantSliceResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants", nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.Len(t, result.Tenants, 1, "expected 1 tenant")
		assert.Equal(t, t1Resp.Tenant.ID, result.Tenants[0].ID, "expected tenant1 id")
		assert.Equal(t, t1Resp.Tenant.Name, result.Tenants[0].Name, "expected tenant1 name")
	})

	t.Run("list subtenants", func(t *testing.T) {
		var result *v1TenantSliceResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants/"+string(t1Resp.Tenant.ID)+"/tenants", nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.Len(t, result.Tenants, 1, "expected 1 tenant")
		assert.Equal(t, t1aResp.Tenant.ID, result.Tenants[0].ID, "expected tenant1.a id")
		assert.Equal(t, t1aResp.Tenant.Name, result.Tenants[0].Name, "expected tenant1.a name")
	})
}

func TestTenantsWithAuth(t *testing.T) {
	testActorID := gidx.MustNewID(TenantIDPrefix)

	oauthClient, issuer, close := echojwtx.TestOAuthClient(string(testActorID), "")
	defer close()

	srv, err := newTestServer(t, &testServerConfig{
		client: oauthClient,
		auth: &echojwtx.AuthConfig{
			Issuer: issuer,
		},
	})
	defer srv.close()

	require.NoError(t, err, "no error expected for new test server")

	t.Run("no tenants", func(t *testing.T) {
		resp, err := srv.RequestWithClient(http.DefaultClient, http.MethodGet, "/v1/tenants", nil, nil, nil)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "unexpected status code returned")

		var result *v1TenantSliceResponse

		resp, err = srv.Request(http.MethodGet, "/v1/tenants", nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.NotNil(t, result, "expected tenants result")
		assert.Equal(t, apiVersion, result.Version, "unexpected response version")
		assert.Len(t, result.Tenants, 0, "expected no tenants")
	})

	subscriber := newPubSubClient(t, srv.logger, srv.nats.ClientURL())
	msgChan := make(chan *nats.Msg, 10)

	// create a new nats subscription on the server created above
	subscription, err := subscriber.ChanSubscribe(
		context.TODO(),
		"com.infratographer.events.tenants.>",
		msgChan,
		"tenant-api-test",
	)

	require.NoError(t, err)

	defer func() {
		if err := subscription.Unsubscribe(); err != nil {
			t.Error(err)
		}
	}()

	var t1Resp *v1TenantResponse

	t.Run("new tenant", func(t *testing.T) {
		createRequest := strings.NewReader(`{"name": "tenant1"}`)

		resp, err := srv.RequestWithClient(http.DefaultClient, http.MethodPost, "/v1/tenants", nil, createRequest, nil)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "unexpected status code returned")

		_, err = createRequest.Seek(0, io.SeekStart)
		assert.NoError(t, err, "no error expected for seek")

		resp, err = srv.Request(http.MethodPost, "/v1/tenants", nil, createRequest, &t1Resp)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for creating tenant")
		assert.Equal(t, http.StatusCreated, resp.StatusCode, "unexpected status code returned")
		assert.NotEmpty(t, t1Resp.Tenant.ID, "expected tenant id")
		assert.Equal(t, "tenant1", t1Resp.Tenant.Name, "unexpected tenant name")

		select {
		case msg := <-msgChan:
			pMsg := &pubsubx.ChangeMessage{}
			err = json.Unmarshal(msg.Data, pMsg)
			require.NoError(t, err)

			assert.Equal(t, tenantSubjectCreate, msg.Subject, "expected nats subject to be tenant create subject")
			assert.Equal(t, testActorID, pMsg.ActorID, "expected auth subject for actor id")
			assert.Equal(t, pubsub.CreateEventType, pMsg.EventType, "expected event type to be create")
			assert.Equal(t, t1Resp.Tenant.ID, pMsg.SubjectID, "expected subject id to be returned tenant id")
		case <-time.After(natsMsgSubTimeout):
			t.Error("failed to receive nats message")
		}
	})

	var t1aResp *v1TenantResponse

	t.Run("new subtenant", func(t *testing.T) {
		createRequest := strings.NewReader(`{"name": "tenant1.a"}`)

		resp, err := srv.RequestWithClient(http.DefaultClient, http.MethodPost, "/v1/tenants/"+string(t1Resp.Tenant.ID)+"/tenants", nil, createRequest, nil)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for creating subtenant")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "unexpected status code returned")

		_, err = createRequest.Seek(0, io.SeekStart)
		assert.NoError(t, err, "no error expected for seek")

		resp, err = srv.Request(http.MethodPost, "/v1/tenants/"+string(t1Resp.Tenant.ID)+"/tenants", nil, createRequest, &t1aResp)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for creating subtenant")
		assert.Equal(t, http.StatusCreated, resp.StatusCode, "unexpected status code returned")
		assert.NotEmpty(t, t1aResp.Tenant.ID, "expected tenant id")
		assert.Equal(t, "tenant1.a", t1aResp.Tenant.Name, "unexpected tenant name")
		require.NotNil(t, t1aResp.Tenant.ParentTenantID, "expected parent tenant id to be set")
		assert.Equal(t, t1Resp.Tenant.ID, *t1aResp.Tenant.ParentTenantID, "unexpected parent tenant id")

		select {
		case msg := <-msgChan:
			pMsg := &pubsubx.ChangeMessage{}
			err = json.Unmarshal(msg.Data, pMsg)
			assert.NoError(t, err)

			assert.Equal(t, tenantSubjectCreate, msg.Subject, "expected nats subject to be tenant create subject")
			assert.Equal(t, testActorID, pMsg.ActorID, "expected auth subject for actor id")
			assert.Equal(t, pubsub.CreateEventType, pMsg.EventType, "expected event type to be create")
			assert.Equal(t, t1aResp.Tenant.ID, pMsg.SubjectID, "expected subject id to be returned tenant id")
			require.NotEmpty(t, pMsg.AdditionalSubjectIDs, "expected additional subject ids")
			assert.Contains(t, pMsg.AdditionalSubjectIDs, t1Resp.Tenant.ID, "expected parent id in additional subject ids")
		case <-time.After(natsMsgSubTimeout):
			t.Error("failed to receive nats message")
		}
	})

	t.Run("list tenants", func(t *testing.T) {
		var result *v1TenantSliceResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants", nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.Len(t, result.Tenants, 1, "expected 1 tenant")
		assert.Equal(t, t1Resp.Tenant.ID, result.Tenants[0].ID, "expected tenant1 id")
		assert.Equal(t, t1Resp.Tenant.Name, result.Tenants[0].Name, "expected tenant1 name")
	})

	t.Run("list subtenants", func(t *testing.T) {
		var result *v1TenantSliceResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants/"+string(t1Resp.Tenant.ID)+"/tenants", nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.Len(t, result.Tenants, 1, "expected 1 tenant")
		assert.Equal(t, t1aResp.Tenant.ID, result.Tenants[0].ID, "expected tenant1.a id")
		assert.Equal(t, t1aResp.Tenant.Name, result.Tenants[0].Name, "expected tenant1.a name")
	})

	t.Run("get tenant", func(t *testing.T) {
		var result *v1TenantResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants/"+string(t1Resp.Tenant.ID), nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.NotEmpty(t, result.Tenant, "expected tenant")
		assert.Equal(t, t1Resp.Tenant.ID, result.Tenant.ID, "expected tenant1 id")
		assert.Equal(t, t1Resp.Tenant.Name, result.Tenant.Name, "expected tenant1 name")
	})

	t.Run("get subtenant", func(t *testing.T) {
		var result *v1TenantResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants/"+string(t1aResp.Tenant.ID), nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.NotEmpty(t, result.Tenant, "expected tenant")
		assert.Equal(t, t1aResp.Tenant.ID, result.Tenant.ID, "expected subtenant id")
		assert.Equal(t, t1aResp.Tenant.Name, result.Tenant.Name, "expected subtenant name")
	})

	t.Run("update tenant", func(t *testing.T) {
		updateRequest := strings.NewReader(`{"name": "tenant1.a-updated"}`)

		resp, err := srv.RequestWithClient(http.DefaultClient, http.MethodPatch, "/v1/tenants/"+string(t1Resp.Tenant.ID), nil, updateRequest, nil)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for updating subtenant")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "unexpected status code returned")

		_, err = updateRequest.Seek(0, io.SeekStart)
		assert.NoError(t, err, "no error expected for seek")

		resp, err = srv.Request(http.MethodPatch, "/v1/tenants/"+string(t1Resp.Tenant.ID), nil, updateRequest, &t1aResp)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for updating subtenant")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")
		assert.NotEmpty(t, t1aResp.Tenant.ID, "expected tenant id")
		assert.Equal(t, "tenant1.a-updated", t1aResp.Tenant.Name, "unexpected tenant name")
		require.NotNil(t, t1aResp.Tenant.ParentTenantID, "expected parent tenant id to be set")
		assert.Equal(t, t1Resp.Tenant.ID, *t1aResp.Tenant.ParentTenantID, "unexpected parent tenant id")

		select {
		case msg := <-msgChan:
			pMsg := &pubsubx.ChangeMessage{}
			err = json.Unmarshal(msg.Data, pMsg)
			assert.NoError(t, err)

			assert.Equal(t, tenantSubjectUpdate, msg.Subject, "expected nats subject to be tenant update subject")
			assert.Equal(t, testActorID, pMsg.ActorID, "expected auth subject for actor id")
			assert.Equal(t, pubsub.UpdateEventType, pMsg.EventType, "expected event type to be update")
			assert.Equal(t, t1aResp.Tenant.ID, pMsg.SubjectID, "expected subject id to be returned tenant id")
			require.Empty(t, pMsg.AdditionalSubjectIDs, "unexpected additional subject ids")
		case <-time.After(natsMsgSubTimeout):
			t.Error("failed to receive nats message")
		}
	})

	t.Run("delete tenant", func(t *testing.T) {
		resp, err := srv.RequestWithClient(http.DefaultClient, http.MethodDelete, "/v1/tenants/"+string(t1Resp.Tenant.ID), nil, nil, nil)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for updating subtenant")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "unexpected status code returned")

		resp, err = srv.Request(http.MethodDelete, "/v1/tenants/"+string(t1Resp.Tenant.ID), nil, nil, nil)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for updating subtenant")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		select {
		case msg := <-msgChan:
			pMsg := &pubsubx.ChangeMessage{}
			err = json.Unmarshal(msg.Data, pMsg)
			assert.NoError(t, err)

			assert.Equal(t, tenantSubjectDelete, msg.Subject, "expected nats subject to be tenant delete subject")
			assert.Equal(t, testActorID, pMsg.ActorID, "expected auth subject for actor id")
			assert.Equal(t, pubsub.DeleteEventType, pMsg.EventType, "expected event type to be delete")
			assert.Equal(t, t1aResp.Tenant.ID, pMsg.SubjectID, "expected subject id to be returned tenant id")
			require.Empty(t, pMsg.AdditionalSubjectIDs, "unexpected additional subject ids")
		case <-time.After(natsMsgSubTimeout):
			t.Error("failed to receive nats message")
		}
	})

	t.Run("get deleted tenant", func(t *testing.T) {
		var result *v1TenantResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants/"+string(t1aResp.Tenant.ID), nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "unexpected status code returned")
	})

	tree := buildTree(t, srv)

	t.Run("list all parents", func(t *testing.T) {
		target := tree.tenantsByName["t1a1b"]

		var result *v1TenantSliceResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants/"+string(target.ID)+"/parents", nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.Len(t, result.Tenants, len(tree.parents[target.ID]), "unexpected tenants returned")

		for _, tenant := range tree.parents[target.ID] {
			assert.Contains(t, tenantIDs(result.Tenants), tenant.ID, "expected tenant to be in response")
		}

		assert.NotContains(t, tenantIDs(result.Tenants), tree.tenantsByName["t2"].ID, "unexpected tree in result")
	})

	t.Run("list parents until", func(t *testing.T) {
		target := tree.tenantsByName["t1a1b"]
		targetParent := tree.tenantsByName["t1a"]

		var result *v1TenantSliceResponse

		resp, err := srv.Request(http.MethodGet, "/v1/tenants/"+string(target.ID)+"/parents/"+string(targetParent.ID), nil, nil, &result)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant list")
		assert.Equal(t, http.StatusOK, resp.StatusCode, "unexpected status code returned")

		require.Len(t, result.Tenants, len(tree.parents[target.ID][1:]), "unexpected tenants returned")

		for _, tenant := range tree.parents[target.ID][1:] {
			assert.Contains(t, tenantIDs(result.Tenants), tenant.ID, "expected tenant to be in response")
		}

		assert.NotContains(t, tenantIDs(result.Tenants), tree.tenantsByName["t1"].ID, "unexpected parent in result")
		assert.NotContains(t, tenantIDs(result.Tenants), tree.tenantsByName["t2"].ID, "unexpected tree in result")
	})
}

func tenantIDs(tenants []*tenant) []gidx.PrefixedID {
	ids := make([]gidx.PrefixedID, len(tenants))

	for i, t := range tenants {
		ids[i] = t.ID
	}

	return ids
}

type hierarchy struct {
	tenantsByID   map[gidx.PrefixedID]*tenant
	tenantsByPath map[string]*tenant
	tenantsByName map[string]*tenant
	descendants   map[gidx.PrefixedID][]*tenant
	parents       map[gidx.PrefixedID][]*tenant
}

// buildTree builds a test tree hierarchy of tenants.
func buildTree(t *testing.T, srv *testServer) *hierarchy {
	t.Helper()

	treeDef := []string{
		"t1",
		"t1.t1a",
		"t1.t1a.t1a1",
		"t1.t1a.t1a1.t1a1a",
		"t1.t1a.t1a1.t1a1b",
		"t1.t1b",
		"t1.t1b.t1b1",
		"t1.t1b.t1b1.t1b1a",
		"t2",
		"t2.t2a",
	}

	tree := &hierarchy{
		tenantsByID:   make(map[gidx.PrefixedID]*tenant),
		tenantsByPath: make(map[string]*tenant),
		tenantsByName: make(map[string]*tenant),
		descendants:   make(map[gidx.PrefixedID][]*tenant),
		parents:       make(map[gidx.PrefixedID][]*tenant),
	}

	for _, path := range treeDef {
		createPath := "/v1/tenants"

		parts := strings.Split(path, ".")

		tenantName := parts[len(parts)-1]

		createRequest := fmt.Sprintf(`{"name": "%s"}`, tenantName)

		if len(parts) > 1 {
			createPath += "/" + string(tree.tenantsByName[parts[len(parts)-2]].ID) + "/tenants"
		}

		var tenantResp *v1TenantResponse

		resp, err := srv.Request(http.MethodPost, createPath, nil, strings.NewReader(createRequest), &tenantResp)
		resp.Body.Close() //nolint:errcheck // Not needed
		require.NoError(t, err, "no error expected for tenant creation")
		require.Equal(t, http.StatusCreated, resp.StatusCode, "unexpected status code returned")

		require.NotNil(t, tenantResp, "expected tenant to not be nil")

		tenant := tenantResp.Tenant

		tree.tenantsByID[tenant.ID] = tenant
		tree.tenantsByPath[path] = tenant
		tree.tenantsByName[tenant.Name] = tenant

		for i := 1; i < len(parts)-1; i++ {
			partID := tree.tenantsByName[parts[i-1]].ID
			tree.descendants[partID] = append(tree.descendants[partID], tenant)
		}

		for i := 0; i <= len(parts)-2; i++ {
			tree.parents[tenant.ID] = append(tree.parents[tenant.ID], tree.tenantsByName[parts[i]])
		}
	}

	return tree
}
