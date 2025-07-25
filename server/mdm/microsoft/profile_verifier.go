package microsoft_mdm

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/fleetdm/fleet/v4/server/contexts/ctxerr"
	"github.com/fleetdm/fleet/v4/server/fleet"
	"github.com/fleetdm/fleet/v4/server/mdm"
	"github.com/fleetdm/fleet/v4/server/mdm/microsoft/admx"
	"github.com/fleetdm/fleet/v4/server/mdm/microsoft/wlanxml"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

// LoopOverExpectedHostProfiles loops all the <LocURI> values on all the profiles for a
// given host. It provides to the callback function:
//
// - An `ExpectedMDMProfile` that references the profile owning the LocURI
// - A hash that's unique for each profile/uri combination
// - The LocURI string
// - The data (if any) of the first <Item> element of the current LocURI
func LoopOverExpectedHostProfiles(
	ctx context.Context,
	ds fleet.ProfileVerificationStore,
	host *fleet.Host,
	fn func(profile *fleet.ExpectedMDMProfile, hash, locURI, data string),
) error {
	profileMap, err := ds.GetHostMDMProfilesExpectedForVerification(ctx, host)
	if err != nil {
		return fmt.Errorf("getting host profiles for verification: %w", err)
	}
	for _, expectedProf := range profileMap {
		expanded, err := ds.ExpandEmbeddedSecrets(ctx, string(expectedProf.RawProfile))
		if err != nil {
			return ctxerr.Wrapf(ctx, err, "expanding embedded secrets for profile %s", expectedProf.Name)
		}
		expectedProf.RawProfile = []byte(expanded)
		var prof fleet.SyncMLCmd
		wrappedBytes := fmt.Sprintf("<Atomic>%s</Atomic>", expectedProf.RawProfile)
		if err := xml.Unmarshal([]byte(wrappedBytes), &prof); err != nil {
			return fmt.Errorf("unmarshalling profile %s: %w", expectedProf.Name, err)
		}
		for _, rc := range prof.ReplaceCommands {
			locURI := rc.GetTargetURI()
			data := rc.GetTargetData()
			ref := HashLocURI(expectedProf.Name, locURI)
			fn(expectedProf, ref, locURI, data)
		}
		for _, ac := range prof.AddCommands {
			locURI := ac.GetTargetURI()
			data := ac.GetTargetData()
			ref := HashLocURI(expectedProf.Name, locURI)
			fn(expectedProf, ref, locURI, data)
		}
	}

	return nil
}

// HashLocURI creates a unique, consistent hash for a given profileName +
// locURI combination.
//
// FIXME: the mdm_bridge table decodes CmdID as `int`,
// so we encode the reference as an int32.
func HashLocURI(profileName, locURI string) string {
	hash := fnv.New32a()
	hash.Write([]byte(profileName + locURI))
	return fmt.Sprint(hash.Sum32())
}

// VerifyHostMDMProfiles performs the verification of the MDM profiles installed on a host and
// updates the verification status in the datastore. It is intended to be called by Fleet osquery
// service when the Fleet server ingests host details.
func VerifyHostMDMProfiles(ctx context.Context, logger log.Logger, ds fleet.ProfileVerificationStore, host *fleet.Host,
	rawProfileResultsSyncML []byte,
) error {
	profileResults, err := transformProfileResults(rawProfileResultsSyncML)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "transforming policy results")
	}

	existingProfiles, err := ds.GetHostMDMWindowsProfiles(ctx, host.UUID)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "getting existing windows host profiles")
	}

	verified, missing, err := compareResultsToExpectedProfiles(ctx, logger, ds, host, profileResults, existingProfiles)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "comparing results to expected profiles")
	}

	toFail, toRetry, err := splitMissingProfilesIntoFailAndRetryBuckets(ctx, ds, host, missing, verified)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "splitting missing profiles into fail and retry buckets")
	}

	err = ds.UpdateHostMDMProfilesVerification(ctx, host, slices.Collect(maps.Keys(verified)), toFail, toRetry)
	if err != nil {
		return ctxerr.Wrap(ctx, err, "updating host mdm profiles during verification")
	}
	return nil
}

func splitMissingProfilesIntoFailAndRetryBuckets(ctx context.Context, ds fleet.ProfileVerificationStore, host *fleet.Host,
	missing map[string]struct{},
	verified map[string]struct{},
) ([]string, []string, error) {
	toFail := make([]string, 0, len(missing))
	toRetry := make([]string, 0, len(missing))
	if len(missing) > 0 {
		counts, err := ds.GetHostMDMProfilesRetryCounts(ctx, host)
		if err != nil {
			return nil, nil, fmt.Errorf("getting host profiles retry counts: %w", err)
		}
		retriesByProfileUUID := make(map[string]uint, len(counts))
		for _, r := range counts {
			retriesByProfileUUID[r.ProfileName] = r.Retries
		}
		for key := range missing {
			// if the profile is in missing, we failed to validate at
			// least one LocURI, delete it from the verified map
			delete(verified, key)
			if retriesByProfileUUID[key] < mdm.MaxProfileRetries {
				// if we haven't hit the max retries, we set
				// the host profile status to nil (which causes
				// an install profile command to be enqueued
				// the next time the profile manager cron runs)
				// and increment the retry count
				toRetry = append(toRetry, key)
				continue
			}
			// otherwise we set the host profile status to failed
			toFail = append(toFail, key)
		}
	}
	return toFail, toRetry, nil
}

func compareResultsToExpectedProfiles(ctx context.Context, logger log.Logger, ds fleet.ProfileVerificationStore, host *fleet.Host,
	profileResults profileResultsTransform, existingProfiles []fleet.HostMDMWindowsProfile,
) (verified map[string]struct{}, missing map[string]struct{}, err error) {
	missing = map[string]struct{}{}
	verified = map[string]struct{}{}

	// Map existing profiles for this host by UUID for easier lookup for certain edge cases
	windowsProfilesByID := make(map[string]fleet.HostMDMWindowsProfile, len(existingProfiles))
	for _, existingProfile := range existingProfiles {
		windowsProfilesByID[existingProfile.ProfileUUID] = existingProfile
	}

	err = LoopOverExpectedHostProfiles(ctx, ds, host, func(profile *fleet.ExpectedMDMProfile, ref, locURI, wantData string) {
		// if we didn't get a status for a LocURI, mark the profile as missing.
		gotStatus, ok := profileResults.cmdRefToStatus[ref]
		if !ok {
			missing[profile.Name] = struct{}{}
			return
		}
		// it's okay if we didn't get a result
		gotResults := profileResults.cmdRefToResult[ref]
		// non-200 status don't have results. Consider it failed
		// unless it falls into a special case we know about.
		// TODO: There are likely more to be added
		var equal bool
		switch {
		case !strings.HasPrefix(gotStatus, "2"):
			equal = false
			// For unknown reasons these always return a 404 so mark as equal in that case if
			// the profile is verifying(meaning MDM protocol returned a good status) or verified
			if gotStatus == "404" && (IsADMXInstallConfigOperationCSP(locURI) || IsWin32OrDesktopBridgeADMXCSP(locURI)) {
				if existingProfile, ok := windowsProfilesByID[profile.ProfileUUID]; ok && existingProfile.Status != nil &&
					(*existingProfile.Status == fleet.MDMDeliveryVerified || *existingProfile.Status == fleet.MDMDeliveryVerifying) {
					level.Debug(logger).Log("msg", "ADMX policy install operation or Win32/Desktop Bridge ADMX policy returned 404, marking as verified", "profile_uuid", profile.ProfileUUID, "host_id", host.ID, "locuri", locURI)
					equal = true
				}
			}
		case wantData == gotResults:
			equal = true
		case wlanxml.IsWLANXML(wantData):
			equal, err = wlanxml.Equal(wantData, gotResults)
			if err != nil {
				err = fmt.Errorf("comparing WLAN XML profiles: %w", err)
				return
			}
		case admx.IsADMX(wantData):
			equal, err = admx.Equal(wantData, gotResults)
			if err != nil {
				err = fmt.Errorf("comparing ADMX policies: %w", err)
				return
			}
		}
		if !equal {
			level.Debug(logger).Log("msg", "Windows profile verification failed", "profile", profile.Name, "host_id", host.ID)
			withinGracePeriod := profile.IsWithinGracePeriod(host.DetailUpdatedAt)
			if !withinGracePeriod {
				missing[profile.Name] = struct{}{}
			}
			return
		}
		verified[profile.Name] = struct{}{}
	})
	if err != nil {
		return nil, nil, fmt.Errorf("looping host mdm LocURIs: %w", err)
	}
	return verified, missing, nil
}

type profileResultsTransform struct {
	cmdRefToStatus map[string]string
	cmdRefToResult map[string]string
}

func transformProfileResults(rawProfileResultsSyncML []byte) (profileResultsTransform, error) {
	var syncML fleet.SyncML
	decoder := xml.NewDecoder(bytes.NewReader(rawProfileResultsSyncML))
	// the DLL used by the `mdm_bridge` extension sends the response with
	// <?xml version="1.0" encoding="utf-16"?>, however if you use
	// `charset.NewReaderLabel` it fails to unmarshal (!?) for now, I'm
	// relying on this hack.
	decoder.CharsetReader = func(encoding string, input io.Reader) (io.Reader, error) {
		return input, nil
	}

	if err := decoder.Decode(&syncML); err != nil {
		return profileResultsTransform{}, fmt.Errorf("decoding provided syncML: %w", err)
	}

	// TODO: what if more than one profile has the same
	// target uri but a different value? (product question)
	transform := profileResultsTransform{
		cmdRefToStatus: map[string]string{},
		cmdRefToResult: map[string]string{},
	}
	for _, r := range syncML.GetOrderedCmds() {
		if r.Cmd.CmdRef == nil {
			continue
		}
		ref := *r.Cmd.CmdRef
		if r.Verb == fleet.CmdStatus && r.Cmd.Data != nil {
			transform.cmdRefToStatus[ref] = *r.Cmd.Data
		}

		if r.Verb == fleet.CmdResults {
			transform.cmdRefToResult[ref] = r.Cmd.GetTargetData()
		}
	}
	return transform, nil
}

// These two methods are for detection of ADMX ingestion and Win32/Desktop Bridge ADMX policies.
// Documentation here: https://learn.microsoft.com/en-us/windows/client-management/win32-and-centennial-app-policy-configuration
// For reasons not entirely clear, attempting to use the Get verb to fetch the results of either the
// ADMXInstall operatiion or the config then installed against it will return a 404 so for now the best
// we can do is detect them and mark them as verified.
func IsADMXInstallConfigOperationCSP(locURI string) bool {
	normalizedLocURI := strings.ToLower(locURI)
	return strings.HasPrefix(normalizedLocURI, "./vendor/msft/policy/configoperations/admxinstall/") || strings.HasPrefix(normalizedLocURI, "./device/vendor/msft/policy/configoperations/admxinstall")
}

func IsWin32OrDesktopBridgeADMXCSP(locURI string) bool {
	normalizedLocURI := strings.ToLower(locURI)
	if strings.HasPrefix(normalizedLocURI, "./vendor/msft/policy/config/") || strings.HasPrefix(normalizedLocURI, "./user/vendor/msft/policy/config/") || strings.HasPrefix(normalizedLocURI, "./device/vendor/msft/policy/config/") {
		return strings.Contains(normalizedLocURI, "~")
	}
	return false
}
