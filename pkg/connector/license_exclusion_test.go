package connector

import (
	"context"
	"path/filepath"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/dotc1z"
	"github.com/conductorone/baton-sdk/pkg/sync"
	"github.com/conductorone/baton-sdk/pkg/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// licenseOnlyConnector syncs just the license resource type, so the test runs a
// real SDK sync without mocking the rest of the Tableau API.
type licenseOnlyConnector struct {
	*Connector
}

// Validate skips the site read the mock does not serve.
func (c *licenseOnlyConnector) Validate(_ context.Context) (annotations.Annotations, error) {
	return nil, nil
}

func (c *licenseOnlyConnector) ResourceSyncers(_ context.Context) []connectorbuilder.ResourceSyncerV2 {
	return []connectorbuilder.ResourceSyncerV2{newLicenseBuilder(c.client)}
}

// inProcessClient lets the SDK syncer call a connector server directly. Only
// the RPCs a license-only sync issues are forwarded; any other call panics on
// the nil embedded client, which flags a new syncer dependency.
type inProcessClient struct {
	types.ConnectorClient
	server types.ConnectorServer
}

func (c *inProcessClient) GetMetadata(ctx context.Context, in *v2.ConnectorServiceGetMetadataRequest, _ ...grpc.CallOption) (*v2.ConnectorServiceGetMetadataResponse, error) {
	return c.server.GetMetadata(ctx, in)
}

func (c *inProcessClient) Validate(ctx context.Context, in *v2.ConnectorServiceValidateRequest, _ ...grpc.CallOption) (*v2.ConnectorServiceValidateResponse, error) {
	return c.server.Validate(ctx, in)
}

func (c *inProcessClient) Cleanup(ctx context.Context, in *v2.ConnectorServiceCleanupRequest, _ ...grpc.CallOption) (*v2.ConnectorServiceCleanupResponse, error) {
	return c.server.Cleanup(ctx, in)
}

func (c *inProcessClient) ListResourceTypes(ctx context.Context, in *v2.ResourceTypesServiceListResourceTypesRequest, _ ...grpc.CallOption) (*v2.ResourceTypesServiceListResourceTypesResponse, error) {
	return c.server.ListResourceTypes(ctx, in)
}

func (c *inProcessClient) ListResources(ctx context.Context, in *v2.ResourcesServiceListResourcesRequest, _ ...grpc.CallOption) (*v2.ResourcesServiceListResourcesResponse, error) {
	return c.server.ListResources(ctx, in)
}

func (c *inProcessClient) ListStaticEntitlements(
	ctx context.Context, in *v2.EntitlementsServiceListStaticEntitlementsRequest, _ ...grpc.CallOption,
) (*v2.EntitlementsServiceListStaticEntitlementsResponse, error) {
	return c.server.ListStaticEntitlements(ctx, in)
}

func (c *inProcessClient) ListEntitlements(ctx context.Context, in *v2.EntitlementsServiceListEntitlementsRequest, _ ...grpc.CallOption) (*v2.EntitlementsServiceListEntitlementsResponse, error) {
	return c.server.ListEntitlements(ctx, in)
}

func (c *inProcessClient) ListGrants(ctx context.Context, in *v2.GrantsServiceListGrantsRequest, _ ...grpc.CallOption) (*v2.GrantsServiceListGrantsResponse, error) {
	return c.server.ListGrants(ctx, in)
}

// TestLicenseSync_TierEntitlementsShareExclusionGroup runs a real SDK sync and
// reads the stored entitlements back, so it covers how the SDK stamps the
// static member entitlement onto each tier: one shared, unscoped group with no
// default or order, and entitlement IDs unchanged.
func TestLicenseSync_TierEntitlementsShareExclusionGroup(t *testing.T) {
	ctx := context.Background()

	var puts []recordedPut
	// The mock only serves sign-in and single-user reads: the site read fails, so
	// tiers sync without seats, and the user list 404s, so grants fail soft.
	tc := newLicenseTestClient(t, map[string]string{}, &puts)

	server, err := connectorbuilder.NewConnector(ctx, &licenseOnlyConnector{Connector: &Connector{client: tc}})
	require.NoError(t, err)

	c1zPath := filepath.Join(t.TempDir(), "sync.c1z")
	syncer, err := sync.NewSyncer(ctx, &inProcessClient{server: server}, sync.WithC1ZPath(c1zPath), sync.WithTmpDir(t.TempDir()))
	require.NoError(t, err)
	require.NoError(t, syncer.Sync(ctx))
	require.NoError(t, syncer.Close(ctx))

	store, err := dotc1z.NewStore(ctx, c1zPath, dotc1z.WithReadOnly(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close(ctx) })

	resp, err := store.ListEntitlements(ctx, &v2.EntitlementsServiceListEntitlementsRequest{})
	require.NoError(t, err)

	got := map[string]*v2.EntitlementExclusionGroup{}
	for _, e := range resp.GetList() {
		group := &v2.EntitlementExclusionGroup{}
		annos := annotations.Annotations(e.GetAnnotations())
		ok, err := annos.Pick(group)
		require.NoError(t, err)
		require.True(t, ok, "entitlement %s has no exclusion group", e.GetId())
		got[e.GetId()] = group
	}

	require.Len(t, got, len(licenses))
	for _, id := range []string{"license:creator:member", "license:explorer:member", "license:viewer:member", "license:unlicensed:member"} {
		group, ok := got[id]
		require.True(t, ok, "missing entitlement ID %s", id)
		require.Equal(t, "tableau-license", group.GetExclusionGroupId())
		require.False(t, group.GetScopeToResource(), "group must be shared across tiers, not scoped per resource")
		require.False(t, group.GetIsDefault(), "no fallback tier is authorized")
		require.Zero(t, group.GetOrder(), "no privilege order is authorized")
	}
}
