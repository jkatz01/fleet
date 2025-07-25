# CIS Benchmarks

_Available in Fleet Premium_.

CIS Benchmarks represent the consensus-based effort of cybersecurity experts to help you protect your systems against threats more confidently.
For more information about CIS Benchmarks check out [Center for Internet Security](https://www.cisecurity.org/cis-benchmarks)'s website.

Fleet has implemented native support for CIS Benchmarks for the following platforms:
- macOS 13.0 Ventura
- macOS 14.0 Sonoma
- macOS 15.0 Sequoia
- Windows 10 Enterprise
- Windows 11 Enterprise

[Where possible](#limitations), each CIS Benchmark is implemented with a [policy query](https://fleetdm.com/docs/rest-api/rest-api#policies) in Fleet. 

These policy queries are intended to assess your organization's security posture against the CIS benchmarks. Because the policy queries alone do not remediate security issues, a host may fail a CIS Benchmark policy if there is no device profile or script in place to enforce the setting. By enabling [automations](https://fleetdm.com/guides/automations#basic-article) in Fleet, these policy queries can used as the basis for managing security compliance and remediation in Fleet.

For example, this is the query for  **CIS - Ensure FileVault Is Enabled (MDM Required)**:

```sql
SELECT 1 WHERE 
      EXISTS (
        SELECT 1 FROM managed_policies WHERE 
            domain='com.apple.MCX' AND 
            name='dontAllowFDEDisable' AND 
            (value = 1 OR value = 'true') AND 
            username = ''
        )
      AND NOT EXISTS (
        SELECT 1 FROM managed_policies WHERE 
            domain='com.apple.MCX' AND 
            name='dontAllowFDEDisable' AND 
            (value != 1 AND value != 'true')
        )
      AND EXISTS (
        SELECT 1 FROM disk_encryption WHERE 
            user_uuid IS NOT "" AND 
            filevault_status = 'on' 
        );  
```

This policy is evaluating 2 attributes:

1. Is FileVault currently enabled?
2. Is there a profile in place that prevents FileVault from being disabled?

If either of these conditions fails, the host is considered to be failing the policy.

## How to add CIS Benchmarks

All CIS policies are stored under our restricted licensed folder `ee/cis/`. To easily convert the [CIS benchmarks YAML raw file](https://raw.githubusercontent.com/fleetdm/fleet/refs/heads/main/ee/cis/macos-14/cis-policy-queries.yml) to a YAML array format compatible with Fleet GitOps, follow these steps:

1. Install [yq](https://github.com/mikefarah/yq) if you don't have it already. (yq is a command-line YAML, JSON and XML processor.)
2. Run this Shell script to transform the policies into [Fleet YAML](https://fleetdm.com/docs/configuration/yaml-files):

```
#!/bin/bash
#shellcheck disable=SC2207


# convert.cis.policy.queries.yml @2024 Fleet Device Management


# CIS queries as written here:
#    https://github.com/fleetdm/fleet/blob/main/ee/cis/macos-14/cis-policy-queries.yml
# must be converted to be uploaded via Fleet GitOps.
#
# This script takes as input the YAML from the file linked above & creates a new YAML array compatible with the "Separate file" format documented here:
#    https://fleetdm.com/docs/configuration/yaml-files#separate-file


# get CIS queries raw file from Fleet repo
cisfile='https://raw.githubusercontent.com/fleetdm/fleet/refs/heads/main/ee/cis/macos-14/cis-policy-queries.yml'
cispath='/private/tmp/cis.yml'
# cisspfl='/private/tmp/cis.gitops.yml'

/usr/bin/curl -X GET -LSs "$cisfile" -o "$cispath"


# create CIS benchmark array
IFS=$'\n'
cisarry=($(/opt/homebrew/bin/yq '.spec.name' "$cispath" | /usr/bin/grep -v '\-\-\-'))

for i in "${cisarry[@]}"
do
	cisname="$(/opt/homebrew/bin/yq ".[] | select(.name == \"$i\") | (del(.platforms)) | (del(.purpose)) | (del(.tags)) | (del(.contributors))" "$cispath" | /opt/homebrew/bin/yq eval '.name')"
	cispfrm="$(/opt/homebrew/bin/yq ".[] | select(.name == \"$i\") | (del(.platforms)) | (del(.purpose)) | (del(.tags)) | (del(.contributors))" "$cispath" | /opt/homebrew/bin/yq eval '.platform')"
	cisdscr="$(/opt/homebrew/bin/yq ".[] | select(.name == \"$i\") | (del(.platforms)) | (del(.purpose)) | (del(.tags)) | (del(.contributors))" "$cispath" | /opt/homebrew/bin/yq eval --unwrapScalar=true '.description')"
	cisrslt="$(/opt/homebrew/bin/yq ".[] | select(.name == \"$i\") | (del(.platforms)) | (del(.purpose)) | (del(.tags)) | (del(.contributors))" "$cispath" | /opt/homebrew/bin/yq eval --unwrapScalar=true '.resolution')"
	cisqrry="$(/opt/homebrew/bin/yq ".[] | select(.name == \"$i\") | (del(.platforms)) | (del(.purpose)) | (del(.tags)) | (del(.contributors))" "$cispath" | /opt/homebrew/bin/yq eval --unwrapScalar=true '.query')" 

	printf "name: %s\nplatform: %s\ndescription: |\n%s\nresolution: |\n%s\nquery: |\n%s\n" "$cisname" "$cispfrm" "$cisdscr" "$cisrslt" "$cisqrry" | /usr/bin/sed 's/^/    /g;s/^[[:space:]]*name:/- name:/;s/^[[:space:]]*platform:/  platform:/;s/^[[:space:]]*description:/  description:/;s/^[[:space:]]*resolution:/  resolution:/;s/^[[:space:]]*query:/  query:/'

# set -x
# trap read debug

done
```

3. The converted YAML is written to standard out in the Terminal. Copy/paste the CIS policies you wish to use into your own YAML file and run Fleet GitOps.

If you're using `fleetctl apply`, you can apply the policies to a specific team use the `--policies-team` flag:
```sh
fleetctl apply --policies-team "Workstations" -f cis-policy-queries.yml
```

## Levels 1 and 2
CIS designates various benchmarks as Level 1 or Level 2 to describe the level of thoroughness and burden that each benchmark represents.

Each benchmark is tagged as `CIS_Level1` or `CIS_Level2`. 

### Level 1

Items in this profile intend to:
- be practical and prudent;
- provide a clear security benefit; and
- not inhibit the utility of the technology beyond acceptable means.

### Level 2

This profile extends the "Level 1" profile. Items in this profile exhibit one or more of the following characteristics:
- are intended for environments or use cases where security is paramount or acts as defense in depth measure
- may negatively inhibit the utility or performance of the technology.

## Requirements

Following are the requirements to use the CIS Benchmarks in Fleet:

- Devices must be running [`fleetd`](https://fleetdm.com/docs/using-fleet/orbit), Fleet's lightweight agent.
- Some CIS Benchmarks explicitly involve verifying MDM-based controls, so devices must be enrolled to an MDM solution.
- On macOS, the orbit component of fleetd must have "Full Disk Access", see [Grant Full Disk Access to Osquery on macOS](https://fleetdm.com/guides/enroll-hosts#grant-full-disk-access-to-osquery-on-macos).

## Limitations

Certain benchmarks cannot be automated by a policy in Fleet. For a list of specific benchmarks which are not covered, please visit the README for each benchmark:

- [macOS 13.0 Ventura](https://github.com/fleetdm/fleet/blob/main/ee/cis/macos-13/README.md)
- [macOS 14.0 Sonoma](https://github.com/fleetdm/fleet/blob/main/ee/cis/macos-14/README.md)
- [macos 15.0 Sequoia](https://github.com/fleetdm/fleet/blob/main/ee/cis/macos-15/README.md)
- [Windows 10 Enterprise](https://github.com/fleetdm/fleet/blob/main/ee/cis/win-10/README.md)
- [Windows 11 Enterprise](https://github.com/fleetdm/fleet/blob/main/ee/cis/win-11/README.md)

## Performance testing
In August 2023, we completed [scale testing on 10k Windows hosts and 70k macOS hosts](https://docs.google.com/document/d/1OSpyzMkHjVhG_-EIBkLu7X3hj_XfVASGl3IXIYChpck/edit?usp=sharing). Ultimately, we validated both server and host performance at that scale.

<meta name="category" value="guides">
<meta name="authorGitHubUsername" value="lucasmrod">
<meta name="authorFullName" value="Lucas Rodriguez">
<meta name="publishedOn" value="2024-04-02">
<meta name="articleTitle" value="CIS Benchmarks">
<meta name="description" value="Read about how Fleet's implementation of CIS Benchmarks offers consensus-based cybersecurity guidance.">
