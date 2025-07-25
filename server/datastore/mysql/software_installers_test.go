package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fleetdm/fleet/v4/server/datastore/filesystem"
	"github.com/fleetdm/fleet/v4/server/datastore/mysql/common_mysql"
	"github.com/fleetdm/fleet/v4/server/fleet"
	"github.com/fleetdm/fleet/v4/server/ptr"
	"github.com/fleetdm/fleet/v4/server/test"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSoftwareInstallers(t *testing.T) {
	ds := CreateMySQLDS(t)

	cases := []struct {
		name string
		fn   func(t *testing.T, ds *Datastore)
	}{
		{"SoftwareInstallRequests", testSoftwareInstallRequests},
		{"ListPendingSoftwareInstalls", testListPendingSoftwareInstalls},
		{"GetSoftwareInstallResults", testGetSoftwareInstallResult},
		{"CleanupUnusedSoftwareInstallers", testCleanupUnusedSoftwareInstallers},
		{"BatchSetSoftwareInstallers", testBatchSetSoftwareInstallers},
		{"GetSoftwareInstallerMetadataByTeamAndTitleID", testGetSoftwareInstallerMetadataByTeamAndTitleID},
		{"HasSelfServiceSoftwareInstallers", testHasSelfServiceSoftwareInstallers},
		{"DeleteSoftwareInstallers", testDeleteSoftwareInstallers},
		{"testDeletePendingSoftwareInstallsForPolicy", testDeletePendingSoftwareInstallsForPolicy},
		{"GetHostLastInstallData", testGetHostLastInstallData},
		{"GetOrGenerateSoftwareInstallerTitleID", testGetOrGenerateSoftwareInstallerTitleID},
		{"BatchSetSoftwareInstallersScopedViaLabels", testBatchSetSoftwareInstallersScopedViaLabels},
		{"MatchOrCreateSoftwareInstallerWithAutomaticPolicies", testMatchOrCreateSoftwareInstallerWithAutomaticPolicies},
		{"GetDetailsForUninstallFromExecutionID", testGetDetailsForUninstallFromExecutionID},
		{"GetTeamsWithInstallerByHash", testGetTeamsWithInstallerByHash},
		{"BatchSetSoftwareInstallersSetupExperienceSideEffects", testBatchSetSoftwareInstallersSetupExperienceSideEffects},
		{"EditDeleteSoftwareInstallersActivateNextActivity", testEditDeleteSoftwareInstallersActivateNextActivity},
		{"BatchSetSoftwareInstallersActivateNextActivity", testBatchSetSoftwareInstallersActivateNextActivity},
		{"SaveInstallerUpdatesClearsFleetMaintainedAppID", testSaveInstallerUpdatesClearsFleetMaintainedAppID},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer TruncateTables(t, ds)
			c.fn(t, ds)
		})
	}
}

func testListPendingSoftwareInstalls(t *testing.T, ds *Datastore) {
	ctx := context.Background()
	t.Cleanup(func() { ds.testActivateSpecificNextActivities = nil })

	host1 := test.NewHost(t, ds, "host1", "1", "host1key", "host1uuid", time.Now())
	host2 := test.NewHost(t, ds, "host2", "2", "host2key", "host2uuid", time.Now())
	host3 := test.NewHost(t, ds, "host3", "3", "host3key", "host3uuid", time.Now())
	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	err := ds.UpsertSecretVariables(ctx, []fleet.SecretVariable{
		{
			Name:  "RUBBER",
			Value: "DUCKY",
		},
		{
			Name:  "BIG",
			Value: "BIRD",
		},
		{
			Name:  "COOKIE",
			Value: "MONSTER",
		},
	})
	require.NoError(t, err)

	tfr1, err := fleet.NewTempFileReader(strings.NewReader("hello"), t.TempDir)
	require.NoError(t, err)
	installerID1, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallScript:     "hello $FLEET_SECRET_RUBBER",
		PreInstallQuery:   "SELECT 1",
		PostInstallScript: "world $FLEET_SECRET_BIG",
		UninstallScript:   "goodbye $FLEET_SECRET_COOKIE",
		InstallerFile:     tfr1,
		StorageID:         "storage1",
		Filename:          "file1",
		Title:             "file1",
		Version:           "1.0",
		Source:            "apps",
		UserID:            user1.ID,
		ValidatedLabels:   &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	tfr2, err := fleet.NewTempFileReader(strings.NewReader("hello"), t.TempDir)
	require.NoError(t, err)
	installerID2, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallScript:     "world",
		PreInstallQuery:   "SELECT 2",
		PostInstallScript: "hello",
		InstallerFile:     tfr2,
		StorageID:         "storage2",
		Filename:          "file2",
		Title:             "file2",
		Version:           "2.0",
		Source:            "apps",
		UserID:            user1.ID,
		ValidatedLabels:   &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	tfr3, err := fleet.NewTempFileReader(strings.NewReader("hello"), t.TempDir)
	require.NoError(t, err)
	installerID3, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallScript:     "banana",
		PreInstallQuery:   "SELECT 3",
		PostInstallScript: "apple",
		InstallerFile:     tfr3,
		StorageID:         "storage3",
		Filename:          "file3",
		Title:             "file3",
		Version:           "3.0",
		Source:            "apps",
		SelfService:       true,
		UserID:            user1.ID,
		ValidatedLabels:   &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	// ensure that nothing gets automatically activated, we want to control
	// specific activation for this test
	ds.testActivateSpecificNextActivities = []string{"-"}

	hostInstall1, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, installerID1, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	time.Sleep(time.Millisecond)
	hostInstall2, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, installerID2, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	time.Sleep(time.Millisecond)
	hostInstall3, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, installerID1, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	time.Sleep(time.Millisecond)
	hostInstall4, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, installerID2, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	pendingHost1, err := ds.ListPendingSoftwareInstalls(ctx, host1.ID)
	require.NoError(t, err)
	require.Equal(t, 2, len(pendingHost1))
	require.Equal(t, hostInstall1, pendingHost1[0])
	require.Equal(t, hostInstall2, pendingHost1[1])

	pendingHost2, err := ds.ListPendingSoftwareInstalls(ctx, host2.ID)
	require.NoError(t, err)
	require.Equal(t, 2, len(pendingHost2))
	require.Equal(t, hostInstall3, pendingHost2[0])
	require.Equal(t, hostInstall4, pendingHost2[1])

	// activate and set a result for hostInstall4 (installerID2)
	ds.testActivateSpecificNextActivities = []string{hostInstall4}
	_, err = ds.activateNextUpcomingActivity(ctx, ds.writer(ctx), host2.ID, "")
	require.NoError(t, err)

	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:                host2.ID,
		InstallUUID:           hostInstall4,
		InstallScriptExitCode: ptr.Int(0),
	})
	require.NoError(t, err)

	// create a new pending install request on host2 for installerID2
	hostInstall5, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, installerID2, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	ds.testActivateSpecificNextActivities = []string{hostInstall5}
	_, err = ds.activateNextUpcomingActivity(ctx, ds.writer(ctx), host2.ID, "")
	require.NoError(t, err)

	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:                    host2.ID,
		InstallUUID:               hostInstall5,
		PreInstallConditionOutput: ptr.String(""), // pre-install query did not return results, so install failed
	})
	require.NoError(t, err)

	installDetailsList1, err := ds.ListPendingSoftwareInstalls(ctx, host1.ID)
	require.NoError(t, err)
	require.Equal(t, 2, len(installDetailsList1))

	installDetailsList2, err := ds.ListPendingSoftwareInstalls(ctx, host2.ID)
	require.NoError(t, err)
	require.Equal(t, 1, len(installDetailsList2))

	require.Contains(t, installDetailsList1, hostInstall1)
	require.Contains(t, installDetailsList1, hostInstall2)
	require.Contains(t, installDetailsList2, hostInstall3)

	exec1, err := ds.GetSoftwareInstallDetails(ctx, hostInstall1)
	require.NoError(t, err)

	require.Equal(t, host1.ID, exec1.HostID)
	require.Equal(t, hostInstall1, exec1.ExecutionID)
	require.Equal(t, "hello DUCKY", exec1.InstallScript)
	require.Equal(t, "world BIRD", exec1.PostInstallScript)
	require.Equal(t, installerID1, exec1.InstallerID)
	require.Equal(t, "SELECT 1", exec1.PreInstallCondition)
	require.False(t, exec1.SelfService)
	assert.Equal(t, "goodbye MONSTER", exec1.UninstallScript)

	// add a self-service request for installerID3 on host1
	hostInstall6, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, installerID3, fleet.HostSoftwareInstallOptions{SelfService: true})
	require.NoError(t, err)

	ds.testActivateSpecificNextActivities = []string{hostInstall6}
	_, err = ds.activateNextUpcomingActivity(ctx, ds.writer(ctx), host1.ID, "")
	require.NoError(t, err)

	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:                    host1.ID,
		InstallUUID:               hostInstall6,
		PreInstallConditionOutput: ptr.String("output"),
	})
	require.NoError(t, err)

	exec2, err := ds.GetSoftwareInstallDetails(ctx, hostInstall6)
	require.NoError(t, err)

	require.Equal(t, host1.ID, exec2.HostID)
	require.Equal(t, hostInstall6, exec2.ExecutionID)
	require.Equal(t, "banana", exec2.InstallScript)
	require.Equal(t, "apple", exec2.PostInstallScript)
	require.Equal(t, installerID3, exec2.InstallerID)
	require.Equal(t, "SELECT 3", exec2.PreInstallCondition)
	require.True(t, exec2.SelfService)

	// Create install request, don't fulfil it, delete and restore host.
	// Should not appear in list of pending installs for that host.
	_, err = ds.InsertSoftwareInstallRequest(ctx, host3.ID, installerID1, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	err = ds.DeleteHost(ctx, host3.ID)
	require.NoError(t, err)

	err = ds.RestoreMDMApplePendingDEPHost(ctx, host3)
	require.NoError(t, err)

	hostInstalls4, err := ds.ListPendingSoftwareInstalls(ctx, host3.ID)
	require.NoError(t, err)
	require.Empty(t, hostInstalls4)
}

func testSoftwareInstallRequests(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	// create a team
	team, err := ds.NewTeam(ctx, &fleet.Team{Name: "team 2"})
	require.NoError(t, err)

	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	cases := map[string]*uint{
		"no team": nil,
		"team":    &team.ID,
	}

	for tc, teamID := range cases {
		t.Run(tc, func(t *testing.T) {
			// non-existent installer
			si, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, teamID, 1, false)
			var nfe fleet.NotFoundError
			require.ErrorAs(t, err, &nfe)
			require.Nil(t, si)

			installerID, titleID, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
				Title:           "foo",
				Source:          "bar",
				InstallScript:   "echo",
				TeamID:          teamID,
				Filename:        "foo.pkg",
				UserID:          user1.ID,
				ValidatedLabels: &fleet.LabelIdentsWithScope{},
			})
			require.NoError(t, err)
			installerMeta, err := ds.GetSoftwareInstallerMetadataByID(ctx, installerID)
			require.NoError(t, err)

			require.NotNil(t, installerMeta.TitleID)
			require.Equal(t, titleID, *installerMeta.TitleID)

			si, err = ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, teamID, *installerMeta.TitleID, false)
			require.NoError(t, err)
			require.NotNil(t, si)
			require.Equal(t, "foo.pkg", si.Name)

			// non-existent host
			_, err = ds.InsertSoftwareInstallRequest(ctx, 12, si.InstallerID, fleet.HostSoftwareInstallOptions{})
			require.ErrorAs(t, err, &nfe)

			// Host with software install pending
			tag := "-pending_install"
			hostPendingInstall, err := ds.NewHost(ctx, &fleet.Host{
				Hostname:      "macos-test" + tag + tc,
				OsqueryHostID: ptr.String("osquery-macos" + tag + tc),
				NodeKey:       ptr.String("node-key-macos" + tag + tc),
				UUID:          uuid.NewString(),
				Platform:      "darwin",
				TeamID:        teamID,
			})
			require.NoError(t, err)
			_, err = ds.InsertSoftwareInstallRequest(ctx, hostPendingInstall.ID, si.InstallerID, fleet.HostSoftwareInstallOptions{})
			require.NoError(t, err)

			// Host with software install failed
			tag = "-failed_install"
			hostFailedInstall, err := ds.NewHost(ctx, &fleet.Host{
				Hostname:      "macos-test" + tag + tc,
				OsqueryHostID: ptr.String("osquery-macos" + tag + tc),
				NodeKey:       ptr.String("node-key-macos" + tag + tc),
				UUID:          uuid.NewString(),
				Platform:      "darwin",
				TeamID:        teamID,
			})
			require.NoError(t, err)
			execID, err := ds.InsertSoftwareInstallRequest(ctx, hostFailedInstall.ID, si.InstallerID, fleet.HostSoftwareInstallOptions{})
			require.NoError(t, err)
			_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
				HostID:                hostFailedInstall.ID,
				InstallUUID:           execID,
				InstallScriptExitCode: ptr.Int(1),
			})
			require.NoError(t, err)

			// Host with software install successful
			tag = "-installed"
			hostInstalled, err := ds.NewHost(ctx, &fleet.Host{
				Hostname:      "macos-test" + tag + tc,
				OsqueryHostID: ptr.String("osquery-macos" + tag + tc),
				NodeKey:       ptr.String("node-key-macos" + tag + tc),
				UUID:          uuid.NewString(),
				Platform:      "darwin",
				TeamID:        teamID,
			})
			require.NoError(t, err)
			execID, err = ds.InsertSoftwareInstallRequest(ctx, hostInstalled.ID, si.InstallerID, fleet.HostSoftwareInstallOptions{})
			require.NoError(t, err)
			_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
				HostID:                hostInstalled.ID,
				InstallUUID:           execID,
				InstallScriptExitCode: ptr.Int(0),
			})
			require.NoError(t, err)

			// Host with pending uninstall
			tag = "-pending_uninstall"
			hostPendingUninstall, err := ds.NewHost(ctx, &fleet.Host{
				Hostname:      "macos-test" + tag + tc,
				OsqueryHostID: ptr.String("osquery-macos" + tag + tc),
				NodeKey:       ptr.String("node-key-macos" + tag + tc),
				UUID:          uuid.NewString(),
				Platform:      "darwin",
				TeamID:        teamID,
			})
			require.NoError(t, err)
			err = ds.InsertSoftwareUninstallRequest(ctx, "uuid"+tag+tc, hostPendingUninstall.ID, si.InstallerID, false)
			require.NoError(t, err)

			// Host with failed uninstall
			tag = "-failed_uninstall"
			hostFailedUninstall, err := ds.NewHost(ctx, &fleet.Host{
				Hostname:      "macos-test" + tag + tc,
				OsqueryHostID: ptr.String("osquery-macos" + tag + tc),
				NodeKey:       ptr.String("node-key-macos" + tag + tc),
				UUID:          uuid.NewString(),
				Platform:      "darwin",
				TeamID:        teamID,
			})
			require.NoError(t, err)
			execID = "uuid" + tag + tc
			err = ds.InsertSoftwareUninstallRequest(ctx, execID, hostFailedUninstall.ID, si.InstallerID, false)
			require.NoError(t, err)
			_, _, err = ds.SetHostScriptExecutionResult(ctx, &fleet.HostScriptResultPayload{
				HostID:      hostFailedUninstall.ID,
				ExecutionID: execID,
				ExitCode:    1,
			})
			require.NoError(t, err)

			// Host with successful uninstall
			tag = "-uninstalled"
			hostUninstalled, err := ds.NewHost(ctx, &fleet.Host{
				Hostname:      "macos-test" + tag + tc,
				OsqueryHostID: ptr.String("osquery-macos" + tag + tc),
				NodeKey:       ptr.String("node-key-macos" + tag + tc),
				UUID:          uuid.NewString(),
				Platform:      "darwin",
				TeamID:        teamID,
			})
			require.NoError(t, err)
			execID = "uuid" + tag + tc
			err = ds.InsertSoftwareUninstallRequest(ctx, execID, hostUninstalled.ID, si.InstallerID, false)
			require.NoError(t, err)
			_, _, err = ds.SetHostScriptExecutionResult(ctx, &fleet.HostScriptResultPayload{
				HostID:      hostUninstalled.ID,
				ExecutionID: execID,
				ExitCode:    0,
			})
			require.NoError(t, err)

			// Uninstall request with unknown host
			err = ds.InsertSoftwareUninstallRequest(ctx, "uuid"+tag+tc, 99999, si.InstallerID, false)
			assert.ErrorContains(t, err, "Host")

			userTeamFilter := fleet.TeamFilter{
				User: &fleet.User{GlobalRole: ptr.String("admin")},
			}

			// for this test, teamID is nil for no-team, but the ListHosts filter
			// returns "all teams" if TeamFilter = nil, it needs to use TeamFilter =
			// 0 for "no team" only.
			teamFilter := teamID
			if teamFilter == nil {
				teamFilter = ptr.Uint(0)
			}

			// list hosts with software install pending requests
			expectStatus := fleet.SoftwareInstallPending
			hosts, err := ds.ListHosts(ctx, userTeamFilter, fleet.HostListOptions{
				ListOptions:           fleet.ListOptions{PerPage: 100},
				SoftwareTitleIDFilter: installerMeta.TitleID,
				SoftwareStatusFilter:  &expectStatus,
				TeamFilter:            teamFilter,
			})
			require.NoError(t, err)

			// get the names of hosts, useful for debugging
			getHostNames := func(hosts []*fleet.Host) []string {
				hostNames := make([]string, len(hosts))
				for _, h := range hosts {
					hostNames = append(hostNames, h.Hostname)
				}
				return hostNames
			}
			require.Len(t, hosts, 1, getHostNames(hosts))
			require.Equal(t, hostPendingInstall.ID, hosts[0].ID)

			// list hosts with all pending requests
			expectStatus = fleet.SoftwarePending
			hosts, err = ds.ListHosts(ctx, userTeamFilter, fleet.HostListOptions{
				ListOptions:           fleet.ListOptions{PerPage: 100},
				SoftwareTitleIDFilter: installerMeta.TitleID,
				SoftwareStatusFilter:  &expectStatus,
				TeamFilter:            teamFilter,
			})
			require.NoError(t, err)
			require.Len(t, hosts, 2, getHostNames(hosts))
			assert.ElementsMatch(t, []uint{hostPendingInstall.ID, hostPendingUninstall.ID}, []uint{hosts[0].ID, hosts[1].ID})

			// list hosts with software install failed requests
			expectStatus = fleet.SoftwareInstallFailed
			hosts, err = ds.ListHosts(ctx, userTeamFilter, fleet.HostListOptions{
				ListOptions:           fleet.ListOptions{PerPage: 100},
				SoftwareTitleIDFilter: installerMeta.TitleID,
				SoftwareStatusFilter:  &expectStatus,
				TeamFilter:            teamFilter,
			})
			require.NoError(t, err)
			require.Len(t, hosts, 1, getHostNames(hosts))
			assert.ElementsMatch(t, []uint{hostFailedInstall.ID}, []uint{hosts[0].ID})

			// list hosts with all failed requests
			expectStatus = fleet.SoftwareFailed
			hosts, err = ds.ListHosts(ctx, userTeamFilter, fleet.HostListOptions{
				ListOptions:           fleet.ListOptions{PerPage: 100},
				SoftwareTitleIDFilter: installerMeta.TitleID,
				SoftwareStatusFilter:  &expectStatus,
				TeamFilter:            teamFilter,
			})
			require.NoError(t, err)
			require.Len(t, hosts, 2, getHostNames(hosts))
			assert.ElementsMatch(t, []uint{hostFailedInstall.ID, hostFailedUninstall.ID}, []uint{hosts[0].ID, hosts[1].ID})

			// list hosts with software installed
			expectStatus = fleet.SoftwareInstalled
			hosts, err = ds.ListHosts(ctx, userTeamFilter, fleet.HostListOptions{
				ListOptions:           fleet.ListOptions{PerPage: 100},
				SoftwareTitleIDFilter: installerMeta.TitleID,
				SoftwareStatusFilter:  &expectStatus,
				TeamFilter:            teamFilter,
			})
			require.NoError(t, err)
			require.Len(t, hosts, 1, getHostNames(hosts))
			assert.ElementsMatch(t, []uint{hostInstalled.ID}, []uint{hosts[0].ID})

			// list hosts with pending software uninstall requests
			expectStatus = fleet.SoftwareUninstallPending
			hosts, err = ds.ListHosts(ctx, userTeamFilter, fleet.HostListOptions{
				ListOptions:           fleet.ListOptions{PerPage: 100},
				SoftwareTitleIDFilter: installerMeta.TitleID,
				SoftwareStatusFilter:  &expectStatus,
				TeamFilter:            teamFilter,
			})
			require.NoError(t, err)
			require.Len(t, hosts, 1, getHostNames(hosts))
			assert.ElementsMatch(t, []uint{hostPendingUninstall.ID}, []uint{hosts[0].ID})

			// list hosts with failed software uninstall requests
			expectStatus = fleet.SoftwareUninstallFailed
			hosts, err = ds.ListHosts(ctx, userTeamFilter, fleet.HostListOptions{
				ListOptions:           fleet.ListOptions{PerPage: 100},
				SoftwareTitleIDFilter: installerMeta.TitleID,
				SoftwareStatusFilter:  &expectStatus,
				TeamFilter:            teamFilter,
			})
			require.NoError(t, err)
			require.Len(t, hosts, 1, getHostNames(hosts))
			assert.ElementsMatch(t, []uint{hostFailedUninstall.ID}, []uint{hosts[0].ID})

			// list all hosts with the software title that shows up in host_software (after fleetd software query is run)
			hosts, err = ds.ListHosts(ctx, userTeamFilter, fleet.HostListOptions{
				ListOptions:           fleet.ListOptions{PerPage: 100},
				SoftwareTitleIDFilter: installerMeta.TitleID,
				TeamFilter:            teamFilter,
			})
			require.NoError(t, err)
			assert.Empty(t, hosts)

			summary, err := ds.GetSummaryHostSoftwareInstalls(ctx, installerMeta.InstallerID)
			require.NoError(t, err)
			require.Equal(t, fleet.SoftwareInstallerStatusSummary{
				Installed:        1,
				PendingInstall:   1,
				FailedInstall:    1,
				PendingUninstall: 1,
				FailedUninstall:  1,
			}, *summary)
		})
	}
}

func testGetSoftwareInstallResult(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	team, err := ds.NewTeam(ctx, &fleet.Team{Name: "team 2"})
	require.NoError(t, err)
	teamID := team.ID

	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	for _, tc := range []struct {
		name                    string
		expectedStatus          fleet.SoftwareInstallerStatus
		postInstallScriptEC     *int
		preInstallQueryOutput   *string
		installScriptEC         *int
		postInstallScriptOutput *string
		installScriptOutput     *string
	}{
		{
			name:                    "pending install",
			expectedStatus:          fleet.SoftwareInstallPending,
			postInstallScriptOutput: ptr.String("post install output"),
			installScriptOutput:     ptr.String("install output"),
		},
		{
			name:                    "failing install post install script",
			expectedStatus:          fleet.SoftwareInstallFailed,
			postInstallScriptEC:     ptr.Int(1),
			postInstallScriptOutput: ptr.String("post install output"),
			installScriptOutput:     ptr.String("install output"),
		},
		{
			name:                    "failing install install script",
			expectedStatus:          fleet.SoftwareInstallFailed,
			installScriptEC:         ptr.Int(1),
			postInstallScriptOutput: ptr.String("post install output"),
			installScriptOutput:     ptr.String("install output"),
		},
		{
			name:                    "failing install pre install query",
			expectedStatus:          fleet.SoftwareInstallFailed,
			preInstallQueryOutput:   ptr.String(""),
			postInstallScriptOutput: ptr.String("post install output"),
			installScriptOutput:     ptr.String("install output"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// create a host and software installer
			swFilename := "file_" + tc.name + ".pkg"
			installerID, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
				Title:           "foo" + tc.name,
				Source:          "bar" + tc.name,
				InstallScript:   "echo " + tc.name,
				Version:         "1.11",
				TeamID:          &teamID,
				Filename:        swFilename,
				UserID:          user1.ID,
				ValidatedLabels: &fleet.LabelIdentsWithScope{},
			})
			require.NoError(t, err)
			host, err := ds.NewHost(ctx, &fleet.Host{
				Hostname:      "macos-test-" + tc.name,
				ComputerName:  "macos-test-" + tc.name,
				OsqueryHostID: ptr.String("osquery-macos-" + tc.name),
				NodeKey:       ptr.String("node-key-macos-" + tc.name),
				UUID:          uuid.NewString(),
				Platform:      "darwin",
				TeamID:        &teamID,
			})
			require.NoError(t, err)

			beforeInstallRequest := time.Now()
			installUUID, err := ds.InsertSoftwareInstallRequest(ctx, host.ID, installerID, fleet.HostSoftwareInstallOptions{})
			require.NoError(t, err)

			res, err := ds.GetSoftwareInstallResults(ctx, installUUID)
			require.NoError(t, err)
			require.NotNil(t, res.UpdatedAt)
			require.Less(t, beforeInstallRequest, res.CreatedAt)
			createdAt := res.CreatedAt
			require.Less(t, beforeInstallRequest, *res.UpdatedAt)

			beforeInstallResult := time.Now()
			_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
				HostID:                    host.ID,
				InstallUUID:               installUUID,
				PreInstallConditionOutput: tc.preInstallQueryOutput,
				InstallScriptExitCode:     tc.installScriptEC,
				InstallScriptOutput:       tc.installScriptOutput,
				PostInstallScriptExitCode: tc.postInstallScriptEC,
				PostInstallScriptOutput:   tc.postInstallScriptOutput,
			})
			require.NoError(t, err)

			// edit installer to ensure host software install is unaffected
			ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
				_, err = q.ExecContext(ctx, `
					UPDATE software_installers SET filename = 'something different', version = '1.23' WHERE id = ?`,
					installerID)
				require.NoError(t, err)
				return nil
			})

			res, err = ds.GetSoftwareInstallResults(ctx, installUUID)
			require.NoError(t, err)
			require.Equal(t, swFilename, res.SoftwarePackage)

			// delete installer to confirm that we can still access the install record (unless pending)
			err = ds.DeleteSoftwareInstaller(ctx, installerID)
			require.NoError(t, err)

			if tc.expectedStatus == fleet.SoftwareInstallPending { // expect pending to be deleted
				_, err = ds.GetSoftwareInstallResults(ctx, installUUID)
				require.Error(t, err, notFound("HostSoftwareInstallerResult"))
				return
			}

			ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
				// ensure version is not changed, though we don't expose it yet
				var version string
				err := sqlx.GetContext(ctx, q, &version, `SELECT "version" FROM host_software_installs WHERE execution_id = ?`, installUUID)
				require.NoError(t, err)
				require.Equal(t, "1.11", version)

				return nil
			})

			res, err = ds.GetSoftwareInstallResults(ctx, installUUID)
			require.NoError(t, err)

			require.Equal(t, installUUID, res.InstallUUID)
			require.Equal(t, tc.expectedStatus, res.Status)
			require.Equal(t, swFilename, res.SoftwarePackage)
			require.Equal(t, host.ID, res.HostID)
			require.Equal(t, tc.preInstallQueryOutput, res.PreInstallQueryOutput)
			require.Equal(t, tc.postInstallScriptOutput, res.PostInstallScriptOutput)
			require.Equal(t, tc.installScriptOutput, res.Output)
			require.NotNil(t, res.CreatedAt)
			require.Equal(t, createdAt, res.CreatedAt)
			require.NotNil(t, res.UpdatedAt)
			require.Less(t, beforeInstallResult, *res.UpdatedAt)
		})
	}
}

func testCleanupUnusedSoftwareInstallers(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	dir := t.TempDir()
	store, err := filesystem.NewSoftwareInstallerStore(dir)
	require.NoError(t, err)

	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	assertExisting := func(want []string) {
		dirEnts, err := os.ReadDir(filepath.Join(dir, "software-installers"))
		require.NoError(t, err)
		got := make([]string, 0, len(dirEnts))
		for _, de := range dirEnts {
			if de.Type().IsRegular() {
				got = append(got, de.Name())
			}
		}
		require.ElementsMatch(t, want, got)
	}

	// cleanup an empty store
	err = ds.CleanupUnusedSoftwareInstallers(ctx, store, time.Now())
	require.NoError(t, err)
	assertExisting(nil)

	// put an installer and save it in the DB
	ins0 := "installer0"
	ins0File := bytes.NewReader([]byte("installer0"))
	err = store.Put(ctx, ins0, ins0File)
	require.NoError(t, err)
	_, _ = ins0File.Seek(0, 0)
	tfr0, err := fleet.NewTempFileReader(ins0File, t.TempDir)
	require.NoError(t, err)
	assertExisting([]string{ins0})

	swi, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallScript:   "install",
		InstallerFile:   tfr0,
		StorageID:       ins0,
		Filename:        "installer0",
		Title:           "ins0",
		Source:          "apps",
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	assertExisting([]string{ins0})
	err = ds.CleanupUnusedSoftwareInstallers(ctx, store, time.Now())
	require.NoError(t, err)
	assertExisting([]string{ins0})

	// remove it from the DB, will now cleanup
	err = ds.DeleteSoftwareInstaller(ctx, swi)
	require.NoError(t, err)

	// would clean up, but not created before 1m ago
	err = ds.CleanupUnusedSoftwareInstallers(ctx, store, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	assertExisting([]string{ins0})

	// do actual cleanup
	err = ds.CleanupUnusedSoftwareInstallers(ctx, store, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assertExisting(nil)
}

func testBatchSetSoftwareInstallers(t *testing.T, ds *Datastore) {
	ctx := context.Background()
	t.Cleanup(func() { ds.testActivateSpecificNextActivities = nil })

	// create a team
	team, err := ds.NewTeam(ctx, &fleet.Team{Name: t.Name()})
	require.NoError(t, err)

	// create a couple hosts
	host1 := test.NewHost(t, ds, "host1", "1", "host1key", "host1uuid", time.Now())
	host2 := test.NewHost(t, ds, "host2", "2", "host2key", "host2uuid", time.Now())
	err = ds.AddHostsToTeam(ctx, fleet.NewAddHostsToTeamParams(&team.ID, []uint{host1.ID, host2.ID}))
	require.NoError(t, err)
	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	// TODO(roberto): perform better assertions, we should have everything
	// to check that the actual values of everything match.
	assertSoftware := func(wantTitles []fleet.SoftwareTitle) {
		tmFilter := fleet.TeamFilter{User: &fleet.User{GlobalRole: ptr.String(fleet.RoleAdmin)}}
		titles, _, _, err := ds.ListSoftwareTitles(
			ctx,
			fleet.SoftwareTitleListOptions{TeamID: &team.ID},
			tmFilter,
		)
		require.NoError(t, err)
		require.Len(t, titles, len(wantTitles))

		for _, title := range titles {
			meta, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, &team.ID, title.ID, false)
			require.NoError(t, err)
			require.NotNil(t, meta.TitleID)
		}
	}

	// batch set with everything empty
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, nil)
	require.NoError(t, err)
	softwareInstallers, err := ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Empty(t, softwareInstallers)
	assertSoftware(nil)
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{})
	require.NoError(t, err)
	softwareInstallers, err = ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Empty(t, softwareInstallers)
	assertSoftware(nil)

	// add a single installer
	ins0 := "installer0"
	ins0File := bytes.NewReader([]byte("installer0"))
	tfr0, err := fleet.NewTempFileReader(ins0File, t.TempDir)
	require.NoError(t, err)
	maintainedApp, err := ds.UpsertMaintainedApp(ctx, &fleet.MaintainedApp{
		Name:             "Maintained1",
		Slug:             "maintained1",
		Platform:         "darwin",
		UniqueIdentifier: "fleet.maintained1",
	})
	require.NoError(t, err)
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{{
		InstallScript:        "install",
		InstallerFile:        tfr0,
		StorageID:            ins0,
		Filename:             "installer0",
		Title:                "ins0",
		Source:               "apps",
		Version:              "1",
		PreInstallQuery:      "foo",
		UserID:               user1.ID,
		Platform:             "darwin",
		URL:                  "https://example.com",
		ValidatedLabels:      &fleet.LabelIdentsWithScope{},
		BundleIdentifier:     "com.example.ins0",
		FleetMaintainedAppID: ptr.Uint(maintainedApp.ID),
	}})
	require.NoError(t, err)
	softwareInstallers, err = ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Len(t, softwareInstallers, 1)
	require.NotNil(t, softwareInstallers[0].TeamID)
	require.Equal(t, team.ID, *softwareInstallers[0].TeamID)
	require.NotNil(t, softwareInstallers[0].TitleID)
	require.Equal(t, "https://example.com", softwareInstallers[0].URL)
	require.Equal(t, maintainedApp.ID, *softwareInstallers[0].FleetMaintainedAppID)
	assertSoftware([]fleet.SoftwareTitle{
		{Name: ins0, Source: "apps", Browser: ""},
	})

	// add a new installer + ins0 installer
	// mark ins0 as install_during_setup
	ins1 := "installer1"
	ins1File := bytes.NewReader([]byte("installer1"))
	tfr1, err := fleet.NewTempFileReader(ins1File, t.TempDir)
	require.NoError(t, err)
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:      "install",
			InstallerFile:      tfr0,
			StorageID:          ins0,
			Filename:           ins0,
			Title:              ins0,
			Source:             "apps",
			Version:            "1",
			PreInstallQuery:    "select 0 from foo;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example.com",
			InstallDuringSetup: ptr.Bool(true),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:     "install",
			PostInstallScript: "post-install",
			InstallerFile:     tfr1,
			StorageID:         ins1,
			Filename:          ins1,
			Title:             ins1,
			Source:            "apps",
			Version:           "2",
			PreInstallQuery:   "select 1 from bar;",
			UserID:            user1.ID,
			Platform:          "darwin",
			URL:               "https://example2.com",
			ValidatedLabels:   &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)
	softwareInstallers, err = ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Len(t, softwareInstallers, 2)
	require.NotNil(t, softwareInstallers[0].TitleID)
	require.NotNil(t, softwareInstallers[0].TeamID)
	require.Equal(t, team.ID, *softwareInstallers[0].TeamID)
	require.Equal(t, "https://example.com", softwareInstallers[0].URL)
	require.NotNil(t, softwareInstallers[1].TitleID)
	require.NotNil(t, softwareInstallers[1].TeamID)
	require.Equal(t, team.ID, *softwareInstallers[1].TeamID)
	require.Equal(t, "https://example2.com", softwareInstallers[1].URL)
	assertSoftware([]fleet.SoftwareTitle{
		{Name: ins0, Source: "apps", Browser: ""},
		{Name: ins1, Source: "apps", Browser: ""},
	})

	// remove ins0 fails due to install_during_setup
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:     "install",
			PostInstallScript: "post-install",
			InstallerFile:     tfr1,
			StorageID:         ins1,
			Filename:          ins1,
			Title:             ins1,
			Source:            "apps",
			Version:           "2",
			PreInstallQuery:   "select 1 from bar;",
			UserID:            user1.ID,
			ValidatedLabels:   &fleet.LabelIdentsWithScope{},
		},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, errDeleteInstallerInstalledDuringSetup)

	// batch-set both installers again, this time with nil install_during_setup for ins0,
	// will keep it as true.
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:      "install",
			InstallerFile:      tfr0,
			StorageID:          ins0,
			Filename:           ins0,
			Title:              ins0,
			Source:             "apps",
			Version:            "1",
			PreInstallQuery:    "select 0 from foo;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example.com",
			InstallDuringSetup: nil,
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:     "install",
			PostInstallScript: "post-install",
			InstallerFile:     tfr1,
			StorageID:         ins1,
			Filename:          ins1,
			Title:             ins1,
			Source:            "apps",
			Version:           "2",
			PreInstallQuery:   "select 1 from bar;",
			UserID:            user1.ID,
			Platform:          "darwin",
			URL:               "https://example2.com",
			ValidatedLabels:   &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)

	// mark ins0 as NOT install_during_setup
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:      "install",
			InstallerFile:      tfr0,
			StorageID:          ins0,
			Filename:           ins0,
			Title:              ins0,
			Source:             "apps",
			Version:            "1",
			PreInstallQuery:    "select 0 from foo;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example.com",
			InstallDuringSetup: ptr.Bool(false),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:     "install",
			PostInstallScript: "post-install",
			InstallerFile:     tfr1,
			StorageID:         ins1,
			Filename:          ins1,
			Title:             ins1,
			Source:            "apps",
			Version:           "2",
			PreInstallQuery:   "select 1 from bar;",
			UserID:            user1.ID,
			Platform:          "darwin",
			URL:               "https://example2.com",
			ValidatedLabels:   &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)

	// remove ins0
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:     "install",
			PostInstallScript: "post-install",
			InstallerFile:     tfr1,
			StorageID:         ins1,
			Filename:          ins1,
			Title:             ins1,
			Source:            "apps",
			Version:           "2",
			PreInstallQuery:   "select 1 from bar;",
			UserID:            user1.ID,
			ValidatedLabels:   &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)
	softwareInstallers, err = ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Len(t, softwareInstallers, 1)
	require.NotNil(t, softwareInstallers[0].TitleID)
	require.NotNil(t, softwareInstallers[0].TeamID)
	require.Empty(t, softwareInstallers[0].URL)
	assertSoftware([]fleet.SoftwareTitle{
		{Name: ins1, Source: "apps", Browser: ""},
	})

	instDetails1, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, &team.ID, *softwareInstallers[0].TitleID, false)
	require.NoError(t, err)

	// add pending and completed installs for ins1
	_, err = ds.InsertSoftwareInstallRequest(ctx, host1.ID, instDetails1.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	execID2, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, instDetails1.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:                host2.ID,
		InstallUUID:           execID2,
		InstallScriptExitCode: ptr.Int(0),
	})
	require.NoError(t, err)

	summary, err := ds.GetSummaryHostSoftwareInstalls(ctx, instDetails1.InstallerID)
	require.NoError(t, err)
	require.Equal(t, fleet.SoftwareInstallerStatusSummary{Installed: 1, PendingInstall: 1}, *summary)

	// batch-set without changes
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:     "install",
			PostInstallScript: "post-install",
			InstallerFile:     tfr1,
			StorageID:         ins1,
			Filename:          ins1,
			Title:             ins1,
			Source:            "apps",
			Version:           "2",
			PreInstallQuery:   "select 1 from bar;",
			UserID:            user1.ID,
			ValidatedLabels:   &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)

	// installs stats haven't changed
	summary, err = ds.GetSummaryHostSoftwareInstalls(ctx, instDetails1.InstallerID)
	require.NoError(t, err)
	require.Equal(t, fleet.SoftwareInstallerStatusSummary{Installed: 1, PendingInstall: 1}, *summary)

	// remove ins1 and add ins0
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:   "install",
			InstallerFile:   tfr0,
			StorageID:       ins0,
			Filename:        ins0,
			Title:           ins0,
			Source:          "apps",
			Version:         "1",
			PreInstallQuery: "select 0 from foo;",
			UserID:          user1.ID,
			Platform:        "darwin",
			URL:             "https://example.com",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)

	// stats don't report anything about ins1 anymore
	summary, err = ds.GetSummaryHostSoftwareInstalls(ctx, instDetails1.InstallerID)
	require.NoError(t, err)
	require.Equal(t, fleet.SoftwareInstallerStatusSummary{Installed: 0, PendingInstall: 0}, *summary)
	pendingHost1, err := ds.ListPendingSoftwareInstalls(ctx, host1.ID)
	require.NoError(t, err)
	require.Empty(t, pendingHost1)

	// add pending and completed installs for ins0
	softwareInstallers, err = ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Len(t, softwareInstallers, 1)
	instDetails0, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, &team.ID, *softwareInstallers[0].TitleID, false)
	require.NoError(t, err)

	_, err = ds.InsertSoftwareInstallRequest(ctx, host1.ID, instDetails0.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	execID2b, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, instDetails0.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:                host2.ID,
		InstallUUID:           execID2b,
		InstallScriptExitCode: ptr.Int(1),
	})
	require.NoError(t, err)

	pendingHost1, err = ds.ListPendingSoftwareInstalls(ctx, host1.ID)
	require.NoError(t, err)
	require.Len(t, pendingHost1, 1)

	summary, err = ds.GetSummaryHostSoftwareInstalls(ctx, instDetails0.InstallerID)
	require.NoError(t, err)
	require.Equal(t, fleet.SoftwareInstallerStatusSummary{FailedInstall: 1, PendingInstall: 1}, *summary)

	// Add software installer with same name different bundle id
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{{
		InstallScript:    "install",
		InstallerFile:    tfr0,
		StorageID:        ins0,
		Filename:         "installer0",
		Title:            "ins0",
		Source:           "apps",
		Version:          "1",
		PreInstallQuery:  "foo",
		UserID:           user1.ID,
		Platform:         "darwin",
		URL:              "https://example.com",
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
		BundleIdentifier: "com.example.different.ins0",
	}})
	require.NoError(t, err)
	softwareInstallers, err = ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Len(t, softwareInstallers, 1)
	assertSoftware([]fleet.SoftwareTitle{
		{Name: ins0, Source: "apps", Browser: "", BundleIdentifier: ptr.String("com.example.different.ins0")},
	})

	// Add software installer with the same bundle id but different name
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{{
		InstallScript:    "install",
		InstallerFile:    tfr0,
		StorageID:        ins0,
		Filename:         "installer0",
		Title:            "ins0-different",
		Source:           "apps",
		Version:          "1",
		PreInstallQuery:  "foo",
		UserID:           user1.ID,
		Platform:         "darwin",
		URL:              "https://example.com",
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
		BundleIdentifier: "com.example.ins0",
	}})
	require.NoError(t, err)
	softwareInstallers, err = ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Len(t, softwareInstallers, 1)
	assertSoftware([]fleet.SoftwareTitle{
		{Name: "ins0-different", Source: "apps", Browser: "", BundleIdentifier: ptr.String("com.example.ins0")},
	})

	// remove everything
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{})
	require.NoError(t, err)
	softwareInstallers, err = ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Empty(t, softwareInstallers)
	assertSoftware([]fleet.SoftwareTitle{})

	// stats don't report anything about ins0 anymore
	summary, err = ds.GetSummaryHostSoftwareInstalls(ctx, instDetails0.InstallerID)
	require.NoError(t, err)
	require.Equal(t, fleet.SoftwareInstallerStatusSummary{FailedInstall: 0, PendingInstall: 0}, *summary)
	pendingHost1, err = ds.ListPendingSoftwareInstalls(ctx, host1.ID)
	require.NoError(t, err)
	require.Empty(t, pendingHost1)
}

func testBatchSetSoftwareInstallersSetupExperienceSideEffects(t *testing.T, ds *Datastore) {
	ctx := context.Background()
	t.Cleanup(func() { ds.testActivateSpecificNextActivities = nil })

	// create a team
	team, err := ds.NewTeam(ctx, &fleet.Team{Name: t.Name()})
	require.NoError(t, err)

	// create a host
	host1 := test.NewHost(t, ds, "host1", "1", "host1key", "host1uuid", time.Now())
	err = ds.AddHostsToTeam(ctx, fleet.NewAddHostsToTeamParams(&team.ID, []uint{host1.ID}))
	host1.TeamID = &team.ID
	require.NoError(t, err)
	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	assertSoftware := func(wantTitles []fleet.SoftwareTitle) {
		tmFilter := fleet.TeamFilter{User: &fleet.User{GlobalRole: ptr.String(fleet.RoleAdmin)}}
		titles, _, _, err := ds.ListSoftwareTitles(
			ctx,
			fleet.SoftwareTitleListOptions{TeamID: &team.ID},
			tmFilter,
		)
		require.NoError(t, err)
		require.Len(t, titles, len(wantTitles))

		for _, title := range titles {
			meta, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, &team.ID, title.ID, false)
			require.NoError(t, err)
			require.NotNil(t, meta.TitleID)
		}
	}

	// add two installers
	ins0 := "installer0"
	ins0File := bytes.NewReader([]byte("installer0"))
	tfr0, err := fleet.NewTempFileReader(ins0File, t.TempDir)
	require.NoError(t, err)

	ins1 := "installer1"
	ins1File := bytes.NewReader([]byte("installer1"))
	tfr1, err := fleet.NewTempFileReader(ins1File, t.TempDir)
	require.NoError(t, err)
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:      "install",
			InstallerFile:      tfr0,
			StorageID:          ins0,
			Filename:           ins0,
			Title:              ins0,
			Source:             "apps",
			Version:            "1",
			PreInstallQuery:    "select 0 from foo;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example.com",
			InstallDuringSetup: ptr.Bool(true),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:      "install",
			PostInstallScript:  "post-install",
			InstallerFile:      tfr1,
			StorageID:          ins1,
			Filename:           ins1,
			Title:              ins1,
			Source:             "apps",
			Version:            "2",
			PreInstallQuery:    "select 1 from bar;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example2.com",
			InstallDuringSetup: ptr.Bool(true),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
	})

	require.NoError(t, err)
	softwareInstallers, err := ds.GetSoftwareInstallers(ctx, team.ID)
	require.NoError(t, err)
	require.Len(t, softwareInstallers, 2)
	require.NotNil(t, softwareInstallers[0].TitleID)
	require.NotNil(t, softwareInstallers[0].TeamID)
	require.Equal(t, team.ID, *softwareInstallers[0].TeamID)
	require.Equal(t, "https://example.com", softwareInstallers[0].URL)
	require.NotNil(t, softwareInstallers[1].TitleID)
	require.NotNil(t, softwareInstallers[1].TeamID)
	require.Equal(t, team.ID, *softwareInstallers[1].TeamID)
	require.Equal(t, "https://example2.com", softwareInstallers[1].URL)
	assertSoftware([]fleet.SoftwareTitle{
		{Name: ins0, Source: "apps", Browser: ""},
		{Name: ins1, Source: "apps", Browser: ""},
	})

	// Add setup_experience_status_results for both installers
	_, err = ds.EnqueueSetupExperienceItems(ctx, host1.UUID, *host1.TeamID)
	require.NoError(t, err)

	statuses, err := ds.ListSetupExperienceResultsByHostUUID(ctx, host1.UUID)
	require.NoError(t, err)
	require.Len(t, statuses, 2)

	// Enqueue the actual install requests
	for _, status := range statuses {
		execID, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, *status.SoftwareInstallerID, fleet.HostSoftwareInstallOptions{ForSetupExperience: true})
		require.NoError(t, err)
		status.HostSoftwareInstallsExecutionID = &execID
		status.Status = fleet.SetupExperienceStatusRunning
		err = ds.UpdateSetupExperienceStatusResult(ctx, status)
		require.NoError(t, err)
	}

	// batch-set without changes
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:      "install",
			InstallerFile:      tfr0,
			StorageID:          ins0,
			Filename:           ins0,
			Title:              ins0,
			Source:             "apps",
			Version:            "1",
			PreInstallQuery:    "select 0 from foo;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example.com",
			InstallDuringSetup: ptr.Bool(true),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:      "install",
			PostInstallScript:  "post-install",
			InstallerFile:      tfr1,
			StorageID:          ins1,
			Filename:           ins1,
			Title:              ins1,
			Source:             "apps",
			Version:            "2",
			PreInstallQuery:    "select 1 from bar;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example2.com",
			InstallDuringSetup: ptr.Bool(true),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)

	statuses, err = ds.ListSetupExperienceResultsByHostUUID(ctx, host1.UUID)
	require.NoError(t, err)
	require.Len(t, statuses, 2)

	for _, status := range statuses {
		require.Equal(t, fleet.SetupExperienceStatusRunning, status.Status)
	}

	// batch-set change ins0's install script to update it and cancel the pending install
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:      "install2",
			InstallerFile:      tfr0,
			StorageID:          ins0,
			Filename:           ins0,
			Title:              ins0,
			Source:             "apps",
			Version:            "1",
			PreInstallQuery:    "select 0 from foo;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example.com",
			InstallDuringSetup: ptr.Bool(true),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:      "install",
			PostInstallScript:  "post-install",
			InstallerFile:      tfr1,
			StorageID:          ins1,
			Filename:           ins1,
			Title:              ins1,
			Source:             "apps",
			Version:            "2",
			PreInstallQuery:    "select 1 from bar;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example2.com",
			InstallDuringSetup: ptr.Bool(true),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
	})

	require.NoError(t, err)

	statuses, err = ds.ListSetupExperienceResultsByHostUUID(ctx, host1.UUID)
	require.NoError(t, err)
	require.Len(t, statuses, 2)

	// Verify that ins0's install was cancelled but ins1 is still running
	ins1ExecID := ""
	ins0Found := false
	ins1Found := false
	for _, status := range statuses {
		if status.Name == ins0 {
			assert.False(t, ins0Found, "duplicate ins0 found")
			ins0Found = true
			require.Equal(t, fleet.SetupExperienceStatusCancelled, status.Status)
		} else {
			assert.False(t, ins1Found, "duplicate ins1 found")
			assert.Equal(t, ins1, status.Name)
			require.Equal(t, fleet.SetupExperienceStatusRunning, status.Status)
			require.NotNil(t, status.HostSoftwareInstallsExecutionID)
			ins1ExecID = *status.HostSoftwareInstallsExecutionID
		}
	}

	// activate and set a result for ins1 as if the install completed
	ds.testActivateSpecificNextActivities = []string{ins1ExecID}
	_, err = ds.activateNextUpcomingActivity(ctx, ds.writer(ctx), host1.ID, "")
	require.NoError(t, err)

	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:                host1.ID,
		InstallUUID:           ins1ExecID,
		InstallScriptExitCode: ptr.Int(0),
	})

	require.NoError(t, err)

	// batch-set change ins1's install script to update it. This should do nothing to the setup
	// experience result because the install already completed
	err = ds.BatchSetSoftwareInstallers(ctx, &team.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:      "install2",
			InstallerFile:      tfr0,
			StorageID:          ins0,
			Filename:           ins0,
			Title:              ins0,
			Source:             "apps",
			Version:            "1",
			PreInstallQuery:    "select 0 from foo;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example.com",
			InstallDuringSetup: ptr.Bool(true),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:      "install3",
			PostInstallScript:  "post-install",
			InstallerFile:      tfr1,
			StorageID:          ins1,
			Filename:           ins1,
			Title:              ins1,
			Source:             "apps",
			Version:            "2",
			PreInstallQuery:    "select 1 from bar;",
			UserID:             user1.ID,
			Platform:           "darwin",
			URL:                "https://example2.com",
			InstallDuringSetup: ptr.Bool(true),
			ValidatedLabels:    &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)

	statuses, err = ds.ListSetupExperienceResultsByHostUUID(ctx, host1.UUID)
	require.NoError(t, err)
	require.Len(t, statuses, 2)

	// Verify that ins0's install is still cancelled and ins1 is still running(because it hasn't
	// been updated in the SESR entry yet)
	ins0Found = false
	ins1Found = false
	for _, status := range statuses {
		if status.Name == ins0 {
			assert.False(t, ins0Found, "duplicate ins0 found")
			ins0Found = true
			require.Equal(t, fleet.SetupExperienceStatusCancelled, status.Status)
		} else {
			assert.False(t, ins1Found, "duplicate ins1 found")
			assert.Equal(t, ins1, status.Name)
			require.Equal(t, fleet.SetupExperienceStatusRunning, status.Status)
		}
	}
}

func testGetSoftwareInstallerMetadataByTeamAndTitleID(t *testing.T, ds *Datastore) {
	ctx := context.Background()
	team, err := ds.NewTeam(ctx, &fleet.Team{Name: "team 2"})
	require.NoError(t, err)
	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	installerID, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		Title:             "foo",
		Source:            "bar",
		InstallScript:     "echo install",
		PostInstallScript: "echo post-install",
		PreInstallQuery:   "SELECT 1",
		TeamID:            &team.ID,
		Filename:          "foo.pkg",
		Platform:          "darwin",
		UserID:            user1.ID,
		ValidatedLabels:   &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)
	installerMeta, err := ds.GetSoftwareInstallerMetadataByID(ctx, installerID)
	require.NoError(t, err)
	require.Equal(t, "darwin", installerMeta.Platform)

	metaByTeamAndTitle, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, &team.ID, *installerMeta.TitleID, true)
	require.NoError(t, err)
	require.Equal(t, "echo install", metaByTeamAndTitle.InstallScript)
	require.Equal(t, "echo post-install", metaByTeamAndTitle.PostInstallScript)
	require.EqualValues(t, installerID, metaByTeamAndTitle.InstallerID)
	require.Equal(t, "SELECT 1", metaByTeamAndTitle.PreInstallQuery)

	installerID, _, err = ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		Title:           "bar",
		Source:          "bar",
		InstallScript:   "echo install",
		TeamID:          &team.ID,
		Filename:        "foo.pkg",
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)
	installerMeta, err = ds.GetSoftwareInstallerMetadataByID(ctx, installerID)
	require.NoError(t, err)

	metaByTeamAndTitle, err = ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, &team.ID, *installerMeta.TitleID, true)
	require.NoError(t, err)
	require.Equal(t, "echo install", metaByTeamAndTitle.InstallScript)
	require.Equal(t, "", metaByTeamAndTitle.PostInstallScript)
	require.EqualValues(t, installerID, metaByTeamAndTitle.InstallerID)
	require.Equal(t, "", metaByTeamAndTitle.PreInstallQuery)
}

func testHasSelfServiceSoftwareInstallers(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	team, err := ds.NewTeam(ctx, &fleet.Team{Name: "team 2"})
	require.NoError(t, err)
	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	test.CreateInsertGlobalVPPToken(t, ds)

	const platform = "linux"
	// No installers
	hasSelfService, err := ds.HasSelfServiceSoftwareInstallers(ctx, platform, nil)
	require.NoError(t, err)
	assert.False(t, hasSelfService)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, platform, &team.ID)
	require.NoError(t, err)
	assert.False(t, hasSelfService)

	// Create a non-self service installer
	_, _, err = ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		Title:           "foo",
		Source:          "bar",
		InstallScript:   "echo install",
		TeamID:          &team.ID,
		Filename:        "foo.pkg",
		Platform:        platform,
		SelfService:     false,
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, platform, nil)
	require.NoError(t, err)
	assert.False(t, hasSelfService)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, platform, &team.ID)
	require.NoError(t, err)
	assert.False(t, hasSelfService)

	// Create a self-service installer for team
	_, _, err = ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		Title:           "foo2",
		Source:          "bar2",
		InstallScript:   "echo install",
		TeamID:          &team.ID,
		Filename:        "foo2.pkg",
		Platform:        platform,
		SelfService:     true,
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, platform, nil)
	require.NoError(t, err)
	assert.False(t, hasSelfService)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, platform, &team.ID)
	require.NoError(t, err)
	assert.True(t, hasSelfService)

	// Create a non self-service VPP for global/linux (not truly possible as VPP is Apple but for testing)
	_, err = ds.InsertVPPAppWithTeam(ctx, &fleet.VPPApp{VPPAppTeam: fleet.VPPAppTeam{VPPAppID: fleet.VPPAppID{AdamID: "adam_vpp_1", Platform: platform}}, Name: "vpp1", BundleIdentifier: "com.app.vpp1"}, nil)
	require.NoError(t, err)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, platform, nil)
	require.NoError(t, err)
	assert.False(t, hasSelfService)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, platform, &team.ID)
	require.NoError(t, err)
	assert.True(t, hasSelfService)

	// Create a self-service VPP for global/linux (not truly possible as VPP is Apple but for testing)
	_, err = ds.InsertVPPAppWithTeam(ctx, &fleet.VPPApp{VPPAppTeam: fleet.VPPAppTeam{VPPAppID: fleet.VPPAppID{AdamID: "adam_vpp_2", Platform: platform}, SelfService: true}, Name: "vpp2", BundleIdentifier: "com.app.vpp2"}, nil)
	require.NoError(t, err)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, platform, nil)
	require.NoError(t, err)
	assert.True(t, hasSelfService)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, platform, &team.ID)
	require.NoError(t, err)
	assert.True(t, hasSelfService)

	// Create a global self-service installer
	_, _, err = ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		Title:           "foo global",
		Source:          "bar",
		InstallScript:   "echo install",
		TeamID:          nil,
		Filename:        "foo global.pkg",
		Platform:        platform,
		SelfService:     true,
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, "ubuntu", nil)
	require.NoError(t, err)
	assert.True(t, hasSelfService)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, "ubuntu", &team.ID)
	require.NoError(t, err)
	assert.True(t, hasSelfService)

	// Create a self-service VPP for team/darwin
	_, err = ds.InsertVPPAppWithTeam(ctx, &fleet.VPPApp{VPPAppTeam: fleet.VPPAppTeam{VPPAppID: fleet.VPPAppID{AdamID: "adam_vpp_3", Platform: fleet.MacOSPlatform}, SelfService: true}, Name: "vpp3", BundleIdentifier: "com.app.vpp3"}, &team.ID)
	require.NoError(t, err)
	// Check darwin
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, "darwin", nil)
	require.NoError(t, err)
	assert.False(t, hasSelfService)
	hasSelfService, err = ds.HasSelfServiceSoftwareInstallers(ctx, "darwin", &team.ID)
	require.NoError(t, err)
	assert.True(t, hasSelfService)
}

func testDeleteSoftwareInstallers(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	dir := t.TempDir()
	store, err := filesystem.NewSoftwareInstallerStore(dir)
	require.NoError(t, err)

	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	// put an installer and save it in the DB
	ins0 := "installer.pkg"
	ins0File := bytes.NewReader([]byte("installer0"))
	err = store.Put(ctx, ins0, ins0File)
	require.NoError(t, err)
	_, _ = ins0File.Seek(0, 0)
	tfr0, err := fleet.NewTempFileReader(ins0File, t.TempDir)
	require.NoError(t, err)

	team1, err := ds.NewTeam(ctx, &fleet.Team{Name: "team1"})
	require.NoError(t, err)

	softwareInstallerID, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallScript:   "install",
		InstallerFile:   tfr0,
		StorageID:       ins0,
		Filename:        "installer.pkg",
		Title:           "ins0",
		Source:          "apps",
		Platform:        "darwin",
		TeamID:          &team1.ID,
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	p1, err := ds.NewTeamPolicy(ctx, team1.ID, &user1.ID, fleet.PolicyPayload{
		Name:                "p1",
		Query:               "SELECT 1;",
		SoftwareInstallerID: &softwareInstallerID,
	})
	require.NoError(t, err)

	err = ds.DeleteSoftwareInstaller(ctx, softwareInstallerID)
	require.Error(t, err)
	require.ErrorIs(t, err, errDeleteInstallerWithAssociatedPolicy)

	_, err = ds.DeleteTeamPolicies(ctx, team1.ID, []uint{p1.ID})
	require.NoError(t, err)

	// mark the installer as "installed during setup", which prevents deletion
	ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
		_, err := q.ExecContext(ctx, `UPDATE software_installers SET install_during_setup = 1 WHERE id = ?`, softwareInstallerID)
		return err
	})

	err = ds.DeleteSoftwareInstaller(ctx, softwareInstallerID)
	require.Error(t, err)
	require.ErrorIs(t, err, errDeleteInstallerInstalledDuringSetup)

	// clear "installed during setup", which allows deletion
	ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
		_, err := q.ExecContext(ctx, `UPDATE software_installers SET install_during_setup = 0 WHERE id = ?`, softwareInstallerID)
		return err
	})

	err = ds.DeleteSoftwareInstaller(ctx, softwareInstallerID)
	require.NoError(t, err)

	// deleting again returns an error, no such installer
	err = ds.DeleteSoftwareInstaller(ctx, softwareInstallerID)
	var nfe *common_mysql.NotFoundError
	require.ErrorAs(t, err, &nfe)
}

func testDeletePendingSoftwareInstallsForPolicy(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	host1 := test.NewHost(t, ds, "host1", "1", "host1key", "host1uuid", time.Now())
	host2 := test.NewHost(t, ds, "host2", "2", "host2key", "host2uuid", time.Now())
	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	team1, err := ds.NewTeam(ctx, &fleet.Team{Name: "team1"})
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := filesystem.NewSoftwareInstallerStore(dir)
	require.NoError(t, err)
	ins0 := "installer.pkg"
	ins0File := bytes.NewReader([]byte("installer0"))
	err = store.Put(ctx, ins0, ins0File)
	require.NoError(t, err)
	_, _ = ins0File.Seek(0, 0)

	tfr0, err := fleet.NewTempFileReader(ins0File, t.TempDir)
	require.NoError(t, err)

	installerID1, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallScript:   "install",
		InstallerFile:   tfr0,
		StorageID:       ins0,
		Filename:        "installer.pkg",
		Title:           "ins0",
		Source:          "apps",
		Platform:        "darwin",
		TeamID:          &team1.ID,
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	policy1, err := ds.NewTeamPolicy(ctx, team1.ID, &user1.ID, fleet.PolicyPayload{
		Name:                "p1",
		Query:               "SELECT 1;",
		SoftwareInstallerID: &installerID1,
	})
	require.NoError(t, err)

	installerID2, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallScript:   "install",
		InstallerFile:   tfr0,
		StorageID:       ins0,
		Filename:        "installer.pkg",
		Title:           "ins1",
		Source:          "apps",
		Platform:        "darwin",
		TeamID:          &team1.ID,
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	policy2, err := ds.NewTeamPolicy(ctx, team1.ID, &user1.ID, fleet.PolicyPayload{
		Name:                "p2",
		Query:               "SELECT 2;",
		SoftwareInstallerID: &installerID2,
	})
	require.NoError(t, err)

	const hostSoftwareInstallsCount = "SELECT count(1) FROM host_software_installs WHERE status = ? and execution_id = ?"
	var count int

	// install for correct policy & correct status
	executionID, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, installerID1, fleet.HostSoftwareInstallOptions{PolicyID: &policy1.ID})
	require.NoError(t, err)

	err = sqlx.GetContext(ctx, ds.reader(ctx), &count, hostSoftwareInstallsCount, fleet.SoftwareInstallPending, executionID)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	err = ds.deletePendingSoftwareInstallsForPolicy(ctx, &team1.ID, policy1.ID)
	require.NoError(t, err)

	err = sqlx.GetContext(ctx, ds.reader(ctx), &count, hostSoftwareInstallsCount, fleet.SoftwareInstallPending, executionID)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	// install for different policy & correct status
	executionID, err = ds.InsertSoftwareInstallRequest(ctx, host1.ID, installerID2, fleet.HostSoftwareInstallOptions{PolicyID: &policy2.ID})
	require.NoError(t, err)

	err = sqlx.GetContext(ctx, ds.reader(ctx), &count, hostSoftwareInstallsCount, fleet.SoftwareInstallPending, executionID)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	err = ds.deletePendingSoftwareInstallsForPolicy(ctx, &team1.ID, policy1.ID)
	require.NoError(t, err)

	err = sqlx.GetContext(ctx, ds.reader(ctx), &count, hostSoftwareInstallsCount, fleet.SoftwareInstallPending, executionID)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// install for correct policy & incorrect status
	executionID, err = ds.InsertSoftwareInstallRequest(ctx, host2.ID, installerID1, fleet.HostSoftwareInstallOptions{PolicyID: &policy1.ID})
	require.NoError(t, err)

	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:                host2.ID,
		InstallUUID:           executionID,
		InstallScriptExitCode: ptr.Int(0),
	})
	require.NoError(t, err)

	err = ds.deletePendingSoftwareInstallsForPolicy(ctx, &team1.ID, policy1.ID)
	require.NoError(t, err)

	err = sqlx.GetContext(ctx, ds.reader(ctx), &count, `SELECT count(1) FROM host_software_installs WHERE execution_id = ?`, executionID)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func testGetHostLastInstallData(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	team1, err := ds.NewTeam(ctx, &fleet.Team{Name: "team1"})
	require.NoError(t, err)
	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	host1 := test.NewHost(t, ds, "host1", "1", "host1key", "host1uuid", time.Now(), test.WithTeamID(team1.ID))
	host2 := test.NewHost(t, ds, "host2", "2", "host2key", "host2uuid", time.Now(), test.WithTeamID(team1.ID))

	dir := t.TempDir()
	store, err := filesystem.NewSoftwareInstallerStore(dir)
	require.NoError(t, err)

	// put an installer and save it in the DB
	ins0 := "installer.pkg"
	ins0File := bytes.NewReader([]byte("installer0"))
	err = store.Put(ctx, ins0, ins0File)
	require.NoError(t, err)
	_, _ = ins0File.Seek(0, 0)
	tfr0, err := fleet.NewTempFileReader(ins0File, t.TempDir)
	require.NoError(t, err)

	softwareInstallerID1, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallScript:   "install",
		InstallerFile:   tfr0,
		StorageID:       ins0,
		Filename:        "installer.pkg",
		Title:           "ins1",
		Source:          "apps",
		Platform:        "darwin",
		TeamID:          &team1.ID,
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)
	softwareInstallerID2, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallScript:   "install2",
		InstallerFile:   tfr0,
		StorageID:       ins0,
		Filename:        "installer2.pkg",
		Title:           "ins2",
		Source:          "apps",
		Platform:        "darwin",
		TeamID:          &team1.ID,
		UserID:          user1.ID,
		ValidatedLabels: &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	// No installations on host1 yet.
	host1LastInstall, err := ds.GetHostLastInstallData(ctx, host1.ID, softwareInstallerID1)
	require.NoError(t, err)
	require.Nil(t, host1LastInstall)

	// Install installer.pkg on host1.
	installUUID1, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, softwareInstallerID1, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, installUUID1)

	// Last installation should be pending.
	host1LastInstall, err = ds.GetHostLastInstallData(ctx, host1.ID, softwareInstallerID1)
	require.NoError(t, err)
	require.NotNil(t, host1LastInstall)
	require.Equal(t, installUUID1, host1LastInstall.ExecutionID)
	require.NotNil(t, host1LastInstall.Status)
	require.Equal(t, fleet.SoftwareInstallPending, *host1LastInstall.Status)

	// Set result of last installation.
	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:      host1.ID,
		InstallUUID: installUUID1,

		InstallScriptExitCode: ptr.Int(0),
	})
	require.NoError(t, err)

	// Last installation should be "installed".
	host1LastInstall, err = ds.GetHostLastInstallData(ctx, host1.ID, softwareInstallerID1)
	require.NoError(t, err)
	require.NotNil(t, host1LastInstall)
	require.Equal(t, installUUID1, host1LastInstall.ExecutionID)
	require.NotNil(t, host1LastInstall.Status)
	require.Equal(t, fleet.SoftwareInstalled, *host1LastInstall.Status)

	// Install installer2.pkg on host1.
	installUUID2, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, softwareInstallerID2, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, installUUID2)

	// Last installation for installer1.pkg should be "installed".
	host1LastInstall, err = ds.GetHostLastInstallData(ctx, host1.ID, softwareInstallerID1)
	require.NoError(t, err)
	require.NotNil(t, host1LastInstall)
	require.Equal(t, installUUID1, host1LastInstall.ExecutionID)
	require.NotNil(t, host1LastInstall.Status)
	require.Equal(t, fleet.SoftwareInstalled, *host1LastInstall.Status)
	// Last installation for installer2.pkg should be "pending".
	host1LastInstall, err = ds.GetHostLastInstallData(ctx, host1.ID, softwareInstallerID2)
	require.NoError(t, err)
	require.NotNil(t, host1LastInstall)
	require.Equal(t, installUUID2, host1LastInstall.ExecutionID)
	require.NotNil(t, host1LastInstall.Status)
	require.Equal(t, fleet.SoftwareInstallPending, *host1LastInstall.Status)

	// Perform another installation of installer1.pkg.
	installUUID3, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, softwareInstallerID1, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, installUUID3)

	// Last installation for installer1.pkg should be "pending" again.
	host1LastInstall, err = ds.GetHostLastInstallData(ctx, host1.ID, softwareInstallerID1)
	require.NoError(t, err)
	require.NotNil(t, host1LastInstall)
	require.Equal(t, installUUID3, host1LastInstall.ExecutionID)
	require.NotNil(t, host1LastInstall.Status)
	require.Equal(t, fleet.SoftwareInstallPending, *host1LastInstall.Status)

	// Set result of last installer1.pkg installation, but first we need to set a
	// result for installUUID2 so that this last installer1.pkg request is
	// activated.
	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:      host1.ID,
		InstallUUID: installUUID2,

		InstallScriptExitCode: ptr.Int(0),
	})
	require.NoError(t, err)
	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:      host1.ID,
		InstallUUID: installUUID3,

		InstallScriptExitCode: ptr.Int(1),
	})
	require.NoError(t, err)

	// Last installation for installer1.pkg should be "failed".
	host1LastInstall, err = ds.GetHostLastInstallData(ctx, host1.ID, softwareInstallerID1)
	require.NoError(t, err)
	require.NotNil(t, host1LastInstall)
	require.Equal(t, installUUID3, host1LastInstall.ExecutionID)
	require.NotNil(t, host1LastInstall.Status)
	require.Equal(t, fleet.SoftwareInstallFailed, *host1LastInstall.Status)

	// No installations on host2.
	host2LastInstall, err := ds.GetHostLastInstallData(ctx, host2.ID, softwareInstallerID1)
	require.NoError(t, err)
	require.Nil(t, host2LastInstall)
	host2LastInstall, err = ds.GetHostLastInstallData(ctx, host2.ID, softwareInstallerID2)
	require.NoError(t, err)
	require.Nil(t, host2LastInstall)
}

func testGetOrGenerateSoftwareInstallerTitleID(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	host1 := test.NewHost(t, ds, "host1", "", "host1key", "host1uuid", time.Now())
	host2 := test.NewHost(t, ds, "host2", "", "host2key", "host2uuid", time.Now())

	software1 := []fleet.Software{
		{Name: "Existing Title", Version: "0.0.1", Source: "apps", BundleIdentifier: "existing.title"},
	}
	software2 := []fleet.Software{
		{Name: "Existing Title", Version: "v0.0.2", Source: "apps", BundleIdentifier: "existing.title"},
		{Name: "Existing Title", Version: "0.0.3", Source: "apps", BundleIdentifier: "existing.title"},
		{Name: "Existing Title Without Bundle", Version: "0.0.3", Source: "apps"},
	}

	_, err := ds.UpdateHostSoftware(ctx, host1.ID, software1)
	require.NoError(t, err)
	_, err = ds.UpdateHostSoftware(ctx, host2.ID, software2)
	require.NoError(t, err)
	require.NoError(t, ds.SyncHostsSoftware(ctx, time.Now()))
	require.NoError(t, ds.ReconcileSoftwareTitles(ctx))
	require.NoError(t, ds.SyncHostsSoftwareTitles(ctx, time.Now()))

	tests := []struct {
		name    string
		payload *fleet.UploadSoftwareInstallerPayload
	}{
		{
			name: "title that already exists, no bundle identifier in payload",
			payload: &fleet.UploadSoftwareInstallerPayload{
				Title:  "Existing Title",
				Source: "apps",
			},
		},
		{
			name: "title that already exists, mismatched bundle identifier in payload",
			payload: &fleet.UploadSoftwareInstallerPayload{
				Title:            "Existing Title",
				Source:           "apps",
				BundleIdentifier: "com.existing.bundle",
			},
		},
		{
			name: "title that already exists but doesn't have a bundle identifier",
			payload: &fleet.UploadSoftwareInstallerPayload{
				Title:  "Existing Title Without Bundle",
				Source: "apps",
			},
		},
		{
			name: "title that already exists, no bundle identifier in DB, bundle identifier in payload",
			payload: &fleet.UploadSoftwareInstallerPayload{
				Title:            "Existing Title Without Bundle",
				Source:           "apps",
				BundleIdentifier: "com.new.bundleid",
			},
		},
		{
			name: "title that doesn't exist, no bundle identifier in payload",
			payload: &fleet.UploadSoftwareInstallerPayload{
				Title:  "New Title",
				Source: "some_source",
			},
		},
		{
			name: "title that doesn't exist, with bundle identifier in payload",
			payload: &fleet.UploadSoftwareInstallerPayload{
				Title:            "New Title With Bundle",
				Source:           "some_source",
				BundleIdentifier: "com.new.bundle",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := ds.getOrGenerateSoftwareInstallerTitleID(ctx, tt.payload)
			require.NoError(t, err)
			require.NotEmpty(t, id)
		})
	}
}

func testBatchSetSoftwareInstallersScopedViaLabels(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	// create a host to have a pending install request
	host := test.NewHost(t, ds, "host1", "1", "host1key", "host1uuid", time.Now())

	// create a couple teams and a user
	tm1, err := ds.NewTeam(ctx, &fleet.Team{Name: t.Name() + "1"})
	require.NoError(t, err)
	tm2, err := ds.NewTeam(ctx, &fleet.Team{Name: t.Name() + "2"})
	require.NoError(t, err)
	user := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	// create some installer payloads to be used by test cases
	installers := make([]*fleet.UploadSoftwareInstallerPayload, 3)
	for i := range installers {
		file := bytes.NewReader([]byte("installer" + fmt.Sprint(i)))
		tfr, err := fleet.NewTempFileReader(file, t.TempDir)
		require.NoError(t, err)
		installers[i] = &fleet.UploadSoftwareInstallerPayload{
			InstallScript:   "install",
			InstallerFile:   tfr,
			StorageID:       "installer" + fmt.Sprint(i),
			Filename:        "installer" + fmt.Sprint(i),
			Title:           "ins" + fmt.Sprint(i),
			Source:          "apps",
			Version:         "1",
			PreInstallQuery: "foo",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com",
		}
	}

	// create some labels to be used by test cases
	labels := make([]*fleet.Label, 4)
	for i := range labels {
		lbl, err := ds.NewLabel(ctx, &fleet.Label{Name: "label" + fmt.Sprint(i)})
		require.NoError(t, err)
		labels[i] = lbl
	}

	type testPayload struct {
		Installer           *fleet.UploadSoftwareInstallerPayload
		Labels              []*fleet.Label
		Exclude             bool
		ShouldCancelPending *bool // nil if the installer is new (could not have pending), otherwise true/false if it was edited
	}

	// test scenarios - note that subtests must NOT be used as the sequence of
	// tests matters - they cannot be run in isolation.
	cases := []struct {
		desc    string
		team    *fleet.Team
		payload []testPayload
	}{
		{
			desc:    "empty payload",
			payload: nil,
		},
		{
			desc: "no team, installer0, no label",
			payload: []testPayload{
				{Installer: installers[0]},
			},
		},
		{
			desc: "team 1, installer0, include label0",
			team: tm1,
			payload: []testPayload{
				{Installer: installers[0], Labels: []*fleet.Label{labels[0]}},
			},
		},
		{
			desc: "no team, installer0 no change, add installer1 with exclude label1",
			payload: []testPayload{
				{Installer: installers[0], ShouldCancelPending: ptr.Bool(false)},
				{Installer: installers[1], Labels: []*fleet.Label{labels[1]}, Exclude: true},
			},
		},
		{
			desc: "no team, installer0 no change, installer1 change to include label1",
			payload: []testPayload{
				{Installer: installers[0], ShouldCancelPending: ptr.Bool(false)},
				{Installer: installers[1], Labels: []*fleet.Label{labels[1]}, Exclude: false, ShouldCancelPending: ptr.Bool(true)},
			},
		},
		{
			desc: "team 1, installer0, include label0 and add label1",
			team: tm1,
			payload: []testPayload{
				{Installer: installers[0], Labels: []*fleet.Label{labels[0], labels[1]}, ShouldCancelPending: ptr.Bool(true)},
			},
		},
		{
			desc: "team 1, installer0, remove label0 and keep label1",
			team: tm1,
			payload: []testPayload{
				{Installer: installers[0], Labels: []*fleet.Label{labels[1]}, ShouldCancelPending: ptr.Bool(true)},
			},
		},
		{
			desc: "team 1, installer0, switch to label0 and label2",
			team: tm1,
			payload: []testPayload{
				{Installer: installers[0], Labels: []*fleet.Label{labels[0], labels[2]}, ShouldCancelPending: ptr.Bool(true)},
			},
		},
		{
			desc: "team 2, 3 installers, mix of labels",
			team: tm2,
			payload: []testPayload{
				{Installer: installers[0], Labels: []*fleet.Label{labels[0]}, Exclude: false},
				{Installer: installers[1], Labels: []*fleet.Label{labels[0], labels[1], labels[2]}, Exclude: true},
				{Installer: installers[2], Labels: []*fleet.Label{labels[1], labels[2]}, Exclude: false},
			},
		},
		{
			desc: "team 1, installer0 no change and add installer2",
			team: tm1,
			payload: []testPayload{
				{Installer: installers[0], Labels: []*fleet.Label{labels[0], labels[2]}, ShouldCancelPending: ptr.Bool(false)},
				{Installer: installers[2]},
			},
		},
		{
			desc: "team 1, installer0 switch to labels 1 and 3, installer2 no change",
			team: tm1,
			payload: []testPayload{
				{Installer: installers[0], Labels: []*fleet.Label{labels[1], labels[3]}, ShouldCancelPending: ptr.Bool(true)},
				{Installer: installers[2], ShouldCancelPending: ptr.Bool(false)},
			},
		},
		{
			desc: "team 2, remove installer0, labels of install1 and no change installer2",
			team: tm2,
			payload: []testPayload{
				{Installer: installers[1], ShouldCancelPending: ptr.Bool(true)},
				{Installer: installers[2], Labels: []*fleet.Label{labels[1], labels[2]}, Exclude: false, ShouldCancelPending: ptr.Bool(false)},
			},
		},
		{
			desc:    "no team, remove all",
			payload: []testPayload{},
		},
	}
	for _, c := range cases {
		t.Log("Running test case ", c.desc)

		var teamID *uint
		var globalOrTeamID uint
		if c.team != nil {
			teamID = &c.team.ID
			globalOrTeamID = c.team.ID
		}

		// cleanup any existing install requests for the host
		ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
			_, err := q.ExecContext(ctx, `DELETE FROM host_software_installs WHERE host_id = ?`, host.ID)
			return err
		})

		installerIDs := make([]uint, len(c.payload))
		if len(c.payload) > 0 {
			// create pending install requests for each updated installer, to see if
			// it cancels it or not as expected.
			err := ds.AddHostsToTeam(ctx, fleet.NewAddHostsToTeamParams(teamID, []uint{host.ID}))
			require.NoError(t, err)
			for i, payload := range c.payload {
				if payload.ShouldCancelPending != nil {
					// the installer must exist
					var swID uint
					ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
						err := sqlx.GetContext(ctx, q, &swID, `SELECT id FROM software_installers WHERE global_or_team_id = ?
						AND title_id IN (SELECT id FROM software_titles WHERE name = ? AND source = ? AND browser = '')`,
							globalOrTeamID, payload.Installer.Title, payload.Installer.Source)
						return err
					})
					_, err = ds.InsertSoftwareInstallRequest(ctx, host.ID, swID, fleet.HostSoftwareInstallOptions{})
					require.NoError(t, err)
					installerIDs[i] = swID
				}
			}
		}

		// create the payload by copying the test one, so that the original installers
		// structs are not modified
		payload := make([]*fleet.UploadSoftwareInstallerPayload, len(c.payload))
		for i, p := range c.payload {
			installer := *p.Installer
			installer.ValidatedLabels = &fleet.LabelIdentsWithScope{LabelScope: fleet.LabelScopeIncludeAny}
			if p.Exclude {
				installer.ValidatedLabels.LabelScope = fleet.LabelScopeExcludeAny
			}
			byName := make(map[string]fleet.LabelIdent, len(p.Labels))
			for _, lbl := range p.Labels {
				byName[lbl.Name] = fleet.LabelIdent{LabelName: lbl.Name, LabelID: lbl.ID}
			}
			installer.ValidatedLabels.ByName = byName
			payload[i] = &installer
		}

		err := ds.BatchSetSoftwareInstallers(ctx, teamID, payload)
		require.NoError(t, err)
		installers, err := ds.GetSoftwareInstallers(ctx, globalOrTeamID)
		require.NoError(t, err)
		require.Len(t, installers, len(c.payload))

		// get the metadata for each installer to assert the batch did set the
		// expected ones.
		installersByFilename := make(map[string]*fleet.SoftwareInstaller, len(installers))
		for _, ins := range installers {
			meta, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, teamID, *ins.TitleID, false)
			require.NoError(t, err)
			installersByFilename[meta.Name] = meta
		}

		// validate that the inserted software is as expected
		for i, payload := range c.payload {
			meta, ok := installersByFilename[payload.Installer.Filename]
			require.True(t, ok, "installer %s was not created", payload.Installer.Filename)
			require.Equal(t, meta.SoftwareTitle, payload.Installer.Title)

			wantLabelIDs := make([]uint, len(payload.Labels))
			for j, lbl := range payload.Labels {
				wantLabelIDs[j] = lbl.ID
			}
			if payload.Exclude {
				require.Empty(t, meta.LabelsIncludeAny)
				gotLabelIDs := make([]uint, len(meta.LabelsExcludeAny))
				for i, lbl := range meta.LabelsExcludeAny {
					gotLabelIDs[i] = lbl.LabelID
				}
				require.ElementsMatch(t, wantLabelIDs, gotLabelIDs)
			} else {
				require.Empty(t, meta.LabelsExcludeAny)
				gotLabelIDs := make([]uint, len(meta.LabelsIncludeAny))
				for j, lbl := range meta.LabelsIncludeAny {
					gotLabelIDs[j] = lbl.LabelID
				}
				require.ElementsMatch(t, wantLabelIDs, gotLabelIDs)
			}

			// check if it deleted pending installs or not
			if payload.ShouldCancelPending != nil {
				lastInstall, err := ds.GetHostLastInstallData(ctx, host.ID, installerIDs[i])
				require.NoError(t, err)
				if *payload.ShouldCancelPending {
					require.Nil(t, lastInstall, "should have cancelled pending installs")
				} else {
					require.NotNil(t, lastInstall, "should not have cancelled pending installs")
				}
			}
		}
	}
}

func testMatchOrCreateSoftwareInstallerWithAutomaticPolicies(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	user1 := test.NewUser(t, ds, "Alice", "alice@example.com", true)
	team1, err := ds.NewTeam(ctx, &fleet.Team{Name: "team 1"})
	require.NoError(t, err)
	team2, err := ds.NewTeam(ctx, &fleet.Team{Name: "team 2"})
	require.NoError(t, err)

	tfr1, err := fleet.NewTempFileReader(strings.NewReader("hello"), t.TempDir)
	require.NoError(t, err)

	// Test pkg without automatic install doesn't create policy.
	_, _, err = ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:    tfr1,
		BundleIdentifier: "com.manual.foobar",
		Extension:        "pkg",
		StorageID:        "storage0",
		Filename:         "foobar0",
		Title:            "Manual foobar",
		Version:          "1.0",
		Source:           "apps",
		UserID:           user1.ID,
		TeamID:           &team1.ID,
		AutomaticInstall: false,
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	team1Policies, _, err := ds.ListTeamPolicies(ctx, team1.ID, fleet.ListOptions{}, fleet.ListOptions{})
	require.NoError(t, err)
	require.Empty(t, team1Policies)

	// Test pkg.
	installerID1, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:    tfr1,
		BundleIdentifier: "com.foo.bar",
		Extension:        "pkg",
		StorageID:        "storage1",
		Filename:         "foobar1",
		Title:            "Foobar",
		Version:          "1.0",
		Source:           "apps",
		UserID:           user1.ID,
		TeamID:           &team1.ID,
		AutomaticInstall: true,
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	team1Policies, _, err = ds.ListTeamPolicies(ctx, team1.ID, fleet.ListOptions{}, fleet.ListOptions{})
	require.NoError(t, err)
	require.Len(t, team1Policies, 1)
	require.Equal(t, "[Install software] Foobar (pkg)", team1Policies[0].Name)
	require.Equal(t, "SELECT 1 FROM apps WHERE bundle_identifier = 'com.foo.bar';", team1Policies[0].Query)
	require.Equal(t, "Policy triggers automatic install of Foobar on each host that's missing this software.", team1Policies[0].Description)
	require.Equal(t, "darwin", team1Policies[0].Platform)
	require.NotNil(t, team1Policies[0].SoftwareInstallerID)
	require.Equal(t, installerID1, *team1Policies[0].SoftwareInstallerID)
	require.NotNil(t, team1Policies[0].TeamID)
	require.Equal(t, team1.ID, *team1Policies[0].TeamID)

	// Test Mac FMA
	fma, err := ds.UpsertMaintainedApp(ctx, &fleet.MaintainedApp{ID: 1})
	require.NoError(t, err)
	installerFMA, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:         tfr1,
		BundleIdentifier:      "com.foo.fma",
		Platform:              "darwin",
		Extension:             "dmg",
		FleetMaintainedAppID:  ptr.Uint(fma.ID),
		StorageID:             "storage1",
		Filename:              "foobar1",
		Title:                 "FooFMA",
		Version:               "1.0",
		Source:                "apps",
		UserID:                user1.ID,
		TeamID:                &team1.ID,
		AutomaticInstall:      true,
		AutomaticInstallQuery: "SELECT 1 FROM osquery_info",
		ValidatedLabels:       &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	team1Policies, _, err = ds.ListTeamPolicies(ctx, team1.ID, fleet.ListOptions{}, fleet.ListOptions{})
	require.NoError(t, err)
	require.Len(t, team1Policies, 2)
	require.Equal(t, "[Install software] FooFMA", team1Policies[1].Name)
	require.Equal(t, "SELECT 1 FROM osquery_info", team1Policies[1].Query)
	require.Equal(t, "Policy triggers automatic install of FooFMA on each host that's missing this software.", team1Policies[1].Description)
	require.Equal(t, "darwin", team1Policies[1].Platform)
	require.NotNil(t, team1Policies[1].SoftwareInstallerID)
	require.Equal(t, installerFMA, *team1Policies[1].SoftwareInstallerID)
	require.NotNil(t, team1Policies[1].TeamID)
	require.Equal(t, team1.ID, *team1Policies[1].TeamID)

	// Test msi.
	installerID2, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:    tfr1,
		Extension:        "msi",
		StorageID:        "storage2",
		Filename:         "zoobar1",
		Title:            "Zoobar",
		Version:          "1.0",
		Source:           "programs",
		UserID:           user1.ID,
		TeamID:           nil,
		AutomaticInstall: true,
		PackageIDs:       []string{"id1"},
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	// check upgrade code handling
	msiPackagesWithNoUpgradeCode, err := ds.GetMSIInstallersWithoutUpgradeCode(ctx)
	require.NoError(t, err)
	require.Equal(t, map[uint]string{installerID2: "storage2"}, msiPackagesWithNoUpgradeCode)
	require.NoError(t, ds.UpdateInstallerUpgradeCode(ctx, installerID2, "upgradecode"))
	msiPackagesWithNoUpgradeCode, err = ds.GetMSIInstallersWithoutUpgradeCode(ctx)
	require.NoError(t, err)
	require.Empty(t, msiPackagesWithNoUpgradeCode)
	msiThatShouldHaveUpgradeCode, err := ds.GetSoftwareInstallerMetadataByID(ctx, installerID2)
	require.NoError(t, err)
	require.Equal(t, "upgradecode", msiThatShouldHaveUpgradeCode.UpgradeCode)

	noTeamPolicies, _, err := ds.ListTeamPolicies(ctx, fleet.PolicyNoTeamID, fleet.ListOptions{}, fleet.ListOptions{})
	require.NoError(t, err)
	require.Len(t, noTeamPolicies, 1)
	require.Equal(t, "[Install software] Zoobar (msi)", noTeamPolicies[0].Name)
	require.Equal(t, "SELECT 1 FROM programs WHERE identifying_number = 'id1';", noTeamPolicies[0].Query)
	require.Equal(t, "Policy triggers automatic install of Zoobar on each host that's missing this software.", noTeamPolicies[0].Description)
	require.Equal(t, "windows", noTeamPolicies[0].Platform)
	require.NotNil(t, noTeamPolicies[0].SoftwareInstallerID)
	require.Equal(t, installerID2, *noTeamPolicies[0].SoftwareInstallerID)
	require.NotNil(t, noTeamPolicies[0].TeamID)
	require.Equal(t, fleet.PolicyNoTeamID, *noTeamPolicies[0].TeamID)

	// Test deb.
	installerID3, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:    tfr1,
		Extension:        "deb",
		StorageID:        "storage3",
		Filename:         "barfoo1",
		Title:            "Barfoo",
		Version:          "1.0",
		Source:           "deb_packages",
		UserID:           user1.ID,
		TeamID:           &team2.ID,
		AutomaticInstall: true,
		PackageIDs:       []string{"id1"},
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	team2Policies, _, err := ds.ListTeamPolicies(ctx, team2.ID, fleet.ListOptions{}, fleet.ListOptions{})
	require.NoError(t, err)
	require.Len(t, team2Policies, 1)
	require.Equal(t, "[Install software] Barfoo (deb)", team2Policies[0].Name)
	require.Equal(t, `SELECT 1 WHERE EXISTS (
	SELECT 1 WHERE (SELECT COUNT(*) FROM deb_packages) = 0
) OR EXISTS (
	SELECT 1 FROM deb_packages WHERE name = 'Barfoo'
);`, team2Policies[0].Query)
	require.Equal(t, `Policy triggers automatic install of Barfoo on each host that's missing this software.
Software won't be installed on Linux hosts with RPM-based distributions because this policy's query is written to always pass on these hosts.`, team2Policies[0].Description)
	require.Equal(t, "linux", team2Policies[0].Platform)
	require.NotNil(t, team2Policies[0].SoftwareInstallerID)
	require.Equal(t, installerID3, *team2Policies[0].SoftwareInstallerID)
	require.NotNil(t, team2Policies[0].TeamID)
	require.Equal(t, team2.ID, *team2Policies[0].TeamID)

	// Test rpm.
	installerID4, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:    tfr1,
		Extension:        "rpm",
		StorageID:        "storage4",
		Filename:         "barzoo1",
		Title:            "Barzoo",
		Version:          "1.0",
		Source:           "rpm_packages",
		UserID:           user1.ID,
		TeamID:           &team2.ID,
		AutomaticInstall: true,
		PackageIDs:       []string{"id1"},
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	team2Policies, _, err = ds.ListTeamPolicies(ctx, team2.ID, fleet.ListOptions{}, fleet.ListOptions{})
	require.NoError(t, err)
	require.Len(t, team2Policies, 2)
	require.Equal(t, "[Install software] Barzoo (rpm)", team2Policies[1].Name)
	require.Equal(t, `SELECT 1 WHERE EXISTS (
	SELECT 1 WHERE (SELECT COUNT(*) FROM rpm_packages) = 0
) OR EXISTS (
	SELECT 1 FROM rpm_packages WHERE name = 'Barzoo'
);`, team2Policies[1].Query)
	require.Equal(t, `Policy triggers automatic install of Barzoo on each host that's missing this software.
Software won't be installed on Linux hosts with Debian-based distributions because this policy's query is written to always pass on these hosts.`, team2Policies[1].Description)
	require.Equal(t, "linux", team2Policies[1].Platform)
	require.NotNil(t, team2Policies[0].SoftwareInstallerID)
	require.Equal(t, installerID4, *team2Policies[1].SoftwareInstallerID)
	require.NotNil(t, team2Policies[1].TeamID)
	require.Equal(t, team2.ID, *team2Policies[1].TeamID)

	_, err = ds.NewTeamPolicy(ctx, team1.ID, &user1.ID, fleet.PolicyPayload{
		Name:  "[Install software] OtherFoobar (pkg)",
		Query: "SELECT 1;",
	})
	require.NoError(t, err)

	// Test pkg and policy with name already exists.
	_, _, err = ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:    tfr1,
		BundleIdentifier: "com.foo2.bar2",
		Extension:        "pkg",
		StorageID:        "storage5",
		Filename:         "foobar5",
		Title:            "OtherFoobar",
		Version:          "2.0",
		Source:           "apps",
		UserID:           user1.ID,
		TeamID:           &team1.ID,
		AutomaticInstall: true,
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	team1Policies, _, err = ds.ListTeamPolicies(ctx, team1.ID, fleet.ListOptions{}, fleet.ListOptions{})
	require.NoError(t, err)
	require.Len(t, team1Policies, 4)
	require.Equal(t, "[Install software] OtherFoobar (pkg) 2", team1Policies[3].Name)

	team3, err := ds.NewTeam(ctx, &fleet.Team{Name: "team 3"})
	require.NoError(t, err)

	_, err = ds.NewTeamPolicy(ctx, team3.ID, &user1.ID, fleet.PolicyPayload{
		Name:  "[Install software] Something2 (msi)",
		Query: "SELECT 1;",
	})
	require.NoError(t, err)
	_, err = ds.NewTeamPolicy(ctx, team3.ID, &user1.ID, fleet.PolicyPayload{
		Name:  "[Install software] Something2 (msi) 2",
		Query: "SELECT 1;",
	})
	require.NoError(t, err)
	// This name is on another team, so it shouldn't count.
	_, err = ds.NewTeamPolicy(ctx, team1.ID, &user1.ID, fleet.PolicyPayload{
		Name:  "[Install software] Something2 (msi) 3",
		Query: "SELECT 1;",
	})
	require.NoError(t, err)

	// Test msi and policy with name already exists.
	_, _, err = ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:    tfr1,
		Extension:        "msi",
		StorageID:        "storage6",
		Filename:         "foobar6",
		Title:            "Something2",
		PackageIDs:       []string{"id2"},
		Version:          "2.0",
		Source:           "programs",
		UserID:           user1.ID,
		TeamID:           &team3.ID,
		AutomaticInstall: true,
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	team3Policies, _, err := ds.ListTeamPolicies(ctx, team3.ID, fleet.ListOptions{}, fleet.ListOptions{})
	require.NoError(t, err)
	require.Len(t, team3Policies, 3)
	require.Equal(t, "[Install software] Something2 (msi) 3", team3Policies[2].Name)
}

func testGetDetailsForUninstallFromExecutionID(t *testing.T, ds *Datastore) {
	ctx := context.Background()

	user := test.NewUser(t, ds, "Alice", "alice@example.com", true)
	host := test.NewHost(t, ds, "host1", "1", "host1key", "host1uuid", time.Now())

	tfr1, err := fleet.NewTempFileReader(strings.NewReader("hello"), t.TempDir)
	require.NoError(t, err)

	// create a couple software titles
	installer1, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:    tfr1,
		BundleIdentifier: "foobar0",
		Extension:        "pkg",
		StorageID:        "storage0",
		Filename:         "foobar0",
		Title:            "foobar",
		Version:          "1.0",
		Source:           "apps",
		UserID:           user.ID,
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	installer2, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		InstallerFile:    tfr1,
		BundleIdentifier: "foobar1",
		Extension:        "pkg",
		StorageID:        "storage1",
		Filename:         "foobar1",
		Title:            "barfoo",
		Version:          "1.0",
		Source:           "apps",
		UserID:           user.ID,
		ValidatedLabels:  &fleet.LabelIdentsWithScope{},
	})
	require.NoError(t, err)

	// get software title for unknown exec id
	title, selfService, err := ds.GetDetailsForUninstallFromExecutionID(ctx, "unknown")
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.Empty(t, title)
	require.False(t, selfService)

	// create a couple pending software install request, the first will be
	// immediately present in host_software_installs too (activated)
	req1, err := ds.InsertSoftwareInstallRequest(ctx, host.ID, installer1, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	req2, err := ds.InsertSoftwareInstallRequest(ctx, host.ID, installer2, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	_, _, err = ds.GetDetailsForUninstallFromExecutionID(ctx, req1)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// record a result for req1, will be deleted from upcoming_activities
	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:                host.ID,
		InstallUUID:           req1,
		InstallScriptExitCode: ptr.Int(0),
	})
	require.NoError(t, err)

	_, _, err = ds.GetDetailsForUninstallFromExecutionID(ctx, req1)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// create an uninstall request for installer1
	req3 := uuid.NewString()
	err = ds.InsertSoftwareUninstallRequest(ctx, req3, host.ID, installer1, true)
	require.NoError(t, err)

	title, selfService, err = ds.GetDetailsForUninstallFromExecutionID(ctx, req3)
	require.NoError(t, err)
	require.Equal(t, "foobar", title)
	require.True(t, selfService)

	// record a result for req2, will activate req3 so it is now in host_software_installs too
	_, err = ds.SetHostSoftwareInstallResult(ctx, &fleet.HostSoftwareInstallResultPayload{
		HostID:                host.ID,
		InstallUUID:           req2,
		InstallScriptExitCode: ptr.Int(0),
	})
	require.NoError(t, err)

	title, selfService, err = ds.GetDetailsForUninstallFromExecutionID(ctx, req3)
	require.NoError(t, err)
	require.Equal(t, "foobar", title)
	require.True(t, selfService)
}

func testGetTeamsWithInstallerByHash(t *testing.T, ds *Datastore) {
	ctx := context.Background()
	user := test.NewUser(t, ds, "Alice", "alice@example.com", true)
	team1, err := ds.NewTeam(ctx, &fleet.Team{Name: "team 1"})
	require.NoError(t, err)
	tfr1, err := fleet.NewTempFileReader(strings.NewReader("hello"), t.TempDir)
	require.NoError(t, err)

	hash1, hash2, hash3 := "hash1", "hash2", "hash3"

	// Add some software installers to No team
	err = ds.BatchSetSoftwareInstallers(ctx, nil, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallerFile:    tfr1,
			BundleIdentifier: "bid1",
			Extension:        "pkg",
			StorageID:        hash1,
			Filename:         "installer1.pkg",
			Title:            "installer1",
			Version:          "1.0",
			Source:           "apps",
			UserID:           user.ID,
			ValidatedLabels:  &fleet.LabelIdentsWithScope{},
			Platform:         "darwin",
			URL:              "https://example.com/1",
		}, {
			InstallerFile:    tfr1,
			BundleIdentifier: "bid2",
			Extension:        "pkg",
			StorageID:        hash2,
			Filename:         "installer2.pkg",
			Title:            "installer2",
			Version:          "2.0",
			Source:           "apps",
			UserID:           user.ID,
			ValidatedLabels:  &fleet.LabelIdentsWithScope{},
			Platform:         "darwin",
			URL:              "https://example.com/2",
		},
	})
	require.NoError(t, err)

	// Add some installers to Team 1
	err = ds.BatchSetSoftwareInstallers(ctx, &team1.ID, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallerFile:    tfr1,
			BundleIdentifier: "bid1",
			Extension:        "pkg",
			StorageID:        hash1,
			Filename:         "installer1.pkg",
			Title:            "installer1",
			Version:          "1.0",
			Source:           "apps",
			UserID:           user.ID,
			ValidatedLabels:  &fleet.LabelIdentsWithScope{},
			TeamID:           &team1.ID,
			Platform:         "darwin",
			URL:              "https://example.com/1",
		},
		{
			InstallerFile:    tfr1,
			BundleIdentifier: "bid3",
			Extension:        "pkg",
			StorageID:        hash3,
			Filename:         "installer3.pkg",
			Title:            "installer3",
			Version:          "3.0",
			Source:           "apps",
			UserID:           user.ID,
			ValidatedLabels:  &fleet.LabelIdentsWithScope{},
			TeamID:           &team1.ID,
			Platform:         "darwin",
			URL:              "https://example.com/4",
		},
	})
	require.NoError(t, err)

	// get installer IDs from added installers
	var installer1NoTeam, installer1Team1, installer2NoTeam uint
	ExecAdhocSQL(t, ds, func(q sqlx.ExtContext) error {
		err := sqlx.GetContext(ctx, q, &installer1NoTeam, "SELECT id FROM software_installers WHERE filename = ? AND global_or_team_id = ?", "installer1.pkg", 0)
		require.NoError(t, err)
		require.NotEmpty(t, installer1NoTeam)

		err = sqlx.GetContext(ctx, q, &installer1Team1, "SELECT id FROM software_installers WHERE filename = ? AND global_or_team_id = ?", "installer1.pkg", team1.ID)
		require.NoError(t, err)
		require.NotEmpty(t, installer1Team1)

		err = sqlx.GetContext(ctx, q, &installer2NoTeam, "SELECT id FROM software_installers WHERE filename = ? AND global_or_team_id = ?", "installer2.pkg", 0)
		require.NoError(t, err)
		require.NotEmpty(t, installer2NoTeam)
		return nil
	})

	// fetching by non-existent hash returns empty map
	installers, err := ds.GetTeamsWithInstallerByHash(ctx, "not_found", "foobar")
	require.NoError(t, err)
	require.Empty(t, installers)

	// there should be 2 installers, one for No team and one for Team 1
	installers, err = ds.GetTeamsWithInstallerByHash(ctx, hash1, "https://example.com/1")
	require.NoError(t, err)
	require.Len(t, installers, 2)

	require.Equal(t, installer1NoTeam, installers[0].InstallerID)
	require.Nil(t, installers[0].TeamID)

	require.Equal(t, installer1Team1, installers[1].InstallerID)
	require.NotNil(t, installers[1].TeamID)
	require.Equal(t, team1.ID, *installers[1].TeamID)

	for _, i := range installers {
		require.Equal(t, "installer1", i.Title)
		require.Equal(t, "pkg", i.Extension)
		require.Equal(t, "1.0", i.Version)
		require.Equal(t, "darwin", i.Platform)
	}

	installers, err = ds.GetTeamsWithInstallerByHash(ctx, hash2, "https://example.com/2")
	require.NoError(t, err)
	require.Len(t, installers, 1)
	require.Equal(t, installers[0].InstallerID, installer2NoTeam)
}

func testEditDeleteSoftwareInstallersActivateNextActivity(t *testing.T, ds *Datastore) {
	ctx := t.Context()
	user := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	// create a few installers
	newInstallerFile := func(ident string) *fleet.TempFileReader {
		tfr, err := fleet.NewTempFileReader(strings.NewReader(ident), t.TempDir)
		require.NoError(t, err)
		return tfr
	}

	err := ds.BatchSetSoftwareInstallers(ctx, nil, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer1"),
			StorageID:       "installer1",
			Filename:        "installer1",
			Title:           "installer1",
			Source:          "apps",
			Version:         "1",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/1",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer2"),
			StorageID:       "installer2",
			Filename:        "installer2",
			Title:           "installer2",
			Source:          "apps",
			Version:         "2",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/2",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)
	installers, err := ds.GetSoftwareInstallers(ctx, 0)
	require.NoError(t, err)
	require.Len(t, installers, 2)
	sort.Slice(installers, func(i, j int) bool {
		return installers[i].URL < installers[j].URL
	})
	ins1, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, nil, *installers[0].TitleID, false)
	require.NoError(t, err)
	ins2, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, nil, *installers[1].TitleID, false)
	require.NoError(t, err)

	// create a few hosts
	host1 := test.NewHost(t, ds, "host1", "1", "host1key", "host1uuid", time.Now())
	host2 := test.NewHost(t, ds, "host2", "2", "host2key", "host2uuid", time.Now())
	host3 := test.NewHost(t, ds, "host3", "3", "host3key", "host3uuid", time.Now())

	// enqueue software installs on each host
	host1Ins1, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, ins1.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host1Ins2, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, ins2.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	// add a script exec as last activity for host1
	host1Script, err := ds.NewHostScriptExecutionRequest(ctx, &fleet.HostScriptRequestPayload{
		HostID: host1.ID, ScriptContents: "echo", UserID: &user.ID, SyncRequest: true,
	})
	require.NoError(t, err)
	host2Ins1, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, ins1.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host2Ins2, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, ins2.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	// add a script exec as first activity for host3
	host3Script, err := ds.NewHostScriptExecutionRequest(ctx, &fleet.HostScriptRequestPayload{
		HostID: host3.ID, ScriptContents: "echo", UserID: &user.ID, SyncRequest: true,
	})
	require.NoError(t, err)
	host3Ins2, err := ds.InsertSoftwareInstallRequest(ctx, host3.ID, ins2.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	checkUpcomingActivities(t, ds, host1, host1Ins1, host1Ins2, host1Script.ExecutionID)
	checkUpcomingActivities(t, ds, host2, host2Ins1, host2Ins2)
	checkUpcomingActivities(t, ds, host3, host3Script.ExecutionID, host3Ins2)

	// simulate an update to installer 1 metadata
	err = ds.ProcessInstallerUpdateSideEffects(ctx, ins1.InstallerID, true, false)
	require.NoError(t, err)

	// installer 1 activities were deleted, next activity was activated
	checkUpcomingActivities(t, ds, host1, host1Ins2, host1Script.ExecutionID)
	checkUpcomingActivities(t, ds, host2, host2Ins2)
	checkUpcomingActivities(t, ds, host3, host3Script.ExecutionID, host3Ins2)

	// delete installer 2
	err = ds.DeleteSoftwareInstaller(ctx, ins2.InstallerID)
	require.NoError(t, err)

	// installer 2 activities were deleted, next activity was activated for host1 and host2
	checkUpcomingActivities(t, ds, host1, host1Script.ExecutionID)
	checkUpcomingActivities(t, ds, host2)
	checkUpcomingActivities(t, ds, host3, host3Script.ExecutionID)
}

func testBatchSetSoftwareInstallersActivateNextActivity(t *testing.T, ds *Datastore) {
	ctx := t.Context()
	user := test.NewUser(t, ds, "Alice", "alice@example.com", true)

	// create a few installers
	newInstallerFile := func(ident string) *fleet.TempFileReader {
		tfr, err := fleet.NewTempFileReader(strings.NewReader(ident), t.TempDir)
		require.NoError(t, err)
		return tfr
	}

	err := ds.BatchSetSoftwareInstallers(ctx, nil, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer1"),
			StorageID:       "installer1",
			Filename:        "installer1",
			Title:           "installer1",
			Source:          "apps",
			Version:         "1",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/1",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer2"),
			StorageID:       "installer2",
			Filename:        "installer2",
			Title:           "installer2",
			Source:          "apps",
			Version:         "2",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/2",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer3"),
			StorageID:       "installer3",
			Filename:        "installer3",
			Title:           "installer3",
			Source:          "apps",
			Version:         "3",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/3",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)
	installers, err := ds.GetSoftwareInstallers(ctx, 0)
	require.NoError(t, err)
	require.Len(t, installers, 3)
	sort.Slice(installers, func(i, j int) bool {
		return installers[i].URL < installers[j].URL
	})
	ins1, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, nil, *installers[0].TitleID, false)
	require.NoError(t, err)
	ins2, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, nil, *installers[1].TitleID, false)
	require.NoError(t, err)
	ins3, err := ds.GetSoftwareInstallerMetadataByTeamAndTitleID(ctx, nil, *installers[2].TitleID, false)
	require.NoError(t, err)

	// create a few hosts
	host1 := test.NewHost(t, ds, "host1", "1", "host1key", "host1uuid", time.Now())
	host2 := test.NewHost(t, ds, "host2", "2", "host2key", "host2uuid", time.Now())
	host3 := test.NewHost(t, ds, "host3", "3", "host3key", "host3uuid", time.Now())

	// enqueue software installs on each host
	host1Ins1, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, ins1.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host1Ins2, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, ins2.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host1Ins3, err := ds.InsertSoftwareInstallRequest(ctx, host1.ID, ins3.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host2Ins2, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, ins2.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host2Ins1, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, ins1.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host2Ins3, err := ds.InsertSoftwareInstallRequest(ctx, host2.ID, ins3.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host3Ins3, err := ds.InsertSoftwareInstallRequest(ctx, host3.ID, ins3.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host3Ins2, err := ds.InsertSoftwareInstallRequest(ctx, host3.ID, ins2.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)
	host3Ins1, err := ds.InsertSoftwareInstallRequest(ctx, host3.ID, ins1.InstallerID, fleet.HostSoftwareInstallOptions{})
	require.NoError(t, err)

	checkUpcomingActivities(t, ds, host1, host1Ins1, host1Ins2, host1Ins3)
	checkUpcomingActivities(t, ds, host2, host2Ins2, host2Ins1, host2Ins3)
	checkUpcomingActivities(t, ds, host3, host3Ins3, host3Ins2, host3Ins1)

	// no change
	err = ds.BatchSetSoftwareInstallers(ctx, nil, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer1"),
			StorageID:       "installer1",
			Filename:        "installer1",
			Title:           "installer1",
			Source:          "apps",
			Version:         "1",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/1",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer2"),
			StorageID:       "installer2",
			Filename:        "installer2",
			Title:           "installer2",
			Source:          "apps",
			Version:         "2",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/2",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer3"),
			StorageID:       "installer3",
			Filename:        "installer3",
			Title:           "installer3",
			Source:          "apps",
			Version:         "3",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/3",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)

	checkUpcomingActivities(t, ds, host1, host1Ins1, host1Ins2, host1Ins3)
	checkUpcomingActivities(t, ds, host2, host2Ins2, host2Ins1, host2Ins3)
	checkUpcomingActivities(t, ds, host3, host3Ins3, host3Ins2, host3Ins1)

	// remove installer 1, update installer 2
	err = ds.BatchSetSoftwareInstallers(ctx, nil, []*fleet.UploadSoftwareInstallerPayload{
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer2"),
			PreInstallQuery: "SELECT 1", // <- metadata updated
			StorageID:       "installer2",
			Filename:        "installer2",
			Title:           "installer2",
			Source:          "apps",
			Version:         "2",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/2",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
		{
			InstallScript:   "install",
			InstallerFile:   newInstallerFile("installer3"),
			StorageID:       "installer3",
			Filename:        "installer3",
			Title:           "installer3",
			Source:          "apps",
			Version:         "3",
			UserID:          user.ID,
			Platform:        "darwin",
			URL:             "https://example.com/3",
			ValidatedLabels: &fleet.LabelIdentsWithScope{},
		},
	})
	require.NoError(t, err)

	// installer 1 and 2 activities were deleted, next activity was activated
	checkUpcomingActivities(t, ds, host1, host1Ins3)
	checkUpcomingActivities(t, ds, host2, host2Ins3)
	checkUpcomingActivities(t, ds, host3, host3Ins3)

	// add a pending script on host 1 and 2
	host1Script, err := ds.NewHostScriptExecutionRequest(ctx, &fleet.HostScriptRequestPayload{
		HostID: host1.ID, ScriptContents: "echo", UserID: &user.ID, SyncRequest: true,
	})
	require.NoError(t, err)
	host2Script, err := ds.NewHostScriptExecutionRequest(ctx, &fleet.HostScriptRequestPayload{
		HostID: host2.ID, ScriptContents: "echo", UserID: &user.ID, SyncRequest: true,
	})
	require.NoError(t, err)

	// clear everything
	err = ds.BatchSetSoftwareInstallers(ctx, nil, []*fleet.UploadSoftwareInstallerPayload{})
	require.NoError(t, err)

	checkUpcomingActivities(t, ds, host1, host1Script.ExecutionID)
	checkUpcomingActivities(t, ds, host2, host2Script.ExecutionID)
	checkUpcomingActivities(t, ds, host3)
}

func testSaveInstallerUpdatesClearsFleetMaintainedAppID(t *testing.T, ds *Datastore) {
	ctx := context.Background()
	user := test.NewUser(t, ds, "Test User", "test@example.com", true)
	tfr, err := fleet.NewTempFileReader(strings.NewReader("file contents"), t.TempDir)
	require.NoError(t, err)

	// Create a maintained app
	maintainedApp, err := ds.UpsertMaintainedApp(ctx, &fleet.MaintainedApp{
		Name:             "Maintained1",
		Slug:             "maintained1",
		Platform:         "darwin",
		UniqueIdentifier: "fleet.maintained1",
	})
	require.NoError(t, err)

	// Create an installer with a non-NULL fleet_maintained_app_id
	installerID, _, err := ds.MatchOrCreateSoftwareInstaller(ctx, &fleet.UploadSoftwareInstallerPayload{
		Title:                "testpkg",
		Source:               "apps",
		InstallScript:        "echo install",
		PreInstallQuery:      "SELECT 1",
		UninstallScript:      "echo uninstall",
		InstallerFile:        tfr,
		StorageID:            "storageid1",
		Filename:             "test.pkg",
		Version:              "1.0",
		UserID:               user.ID,
		ValidatedLabels:      &fleet.LabelIdentsWithScope{},
		FleetMaintainedAppID: ptr.Uint(maintainedApp.ID),
	})
	require.NoError(t, err)

	// Prepare update payload with a new installer file (should clear FMA id)
	installScript := "echo install updated"
	uninstallScript := "echo uninstall updated"
	preInstallQuery := "SELECT 2"
	selfService := true
	payload := &fleet.UpdateSoftwareInstallerPayload{
		InstallerID:     installerID,
		StorageID:       "storageid2", // different storage id
		Filename:        "test2.pkg",
		Version:         "2.0",
		PackageIDs:      []string{"com.test.pkg"},
		InstallScript:   &installScript,
		UninstallScript: &uninstallScript,
		PreInstallQuery: &preInstallQuery,
		SelfService:     &selfService,
		InstallerFile:   tfr, // triggers clearing
		UserID:          user.ID,
	}

	require.NoError(t, ds.SaveInstallerUpdates(ctx, payload))

	// Assert that fleet_maintained_app_id is now NULL
	var fmaID *uint
	err = sqlx.GetContext(ctx, ds.reader(ctx), &fmaID, `SELECT fleet_maintained_app_id FROM software_installers WHERE id = ?`, installerID)
	require.NoError(t, err)
	assert.Nil(t, fmaID, "fleet_maintained_app_id should be NULL after update")
}
