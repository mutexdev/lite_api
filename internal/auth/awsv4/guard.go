package awsv4

// The egress guard: the seam an agent-initiated run uses to authorize, or
// refuse, the side effects credential resolution has on the outside world.
//
// Resolving an AWS profile is not a pure read of ~/.aws. It can POST to STS,
// GET from SSO, refresh a token against SSO-OIDC, and — with
// credential_process — spawn an arbitrary program. A caller that must account
// for every destination a run contacts therefore needs two things from this
// package: a way to know, before the run starts, which endpoints a given
// configuration would reach (CredentialEndpointOrigins), and a way to have the
// last word at each call (EgressGuard, carried on the request context).
//
// The guard is read from the context rather than installed globally like
// SetHTTPClient, because it is per-execution: two sends can be in flight with
// different policies, and a process-wide variable could not tell them apart.
// A context with no guard is permissive, which is the UI path and behaves
// exactly as it did before this existed.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mutexdev/lite_api/internal/types"
)

// EgressGuard authorizes the outbound calls credential resolution makes.
//
// AuthorizeCredentialEndpoint receives the exact URL about to be requested —
// scheme, host, port, path and query — and returns a non-nil error to abort
// before the request is made. Implementations are expected to key on the
// origin; the full URL is passed because throwing information away here would
// be irreversible for the caller.
//
// The mere presence of a guard also refuses credential_process: see
// CredentialProcessRefusedError.
type EgressGuard interface {
	AuthorizeCredentialEndpoint(ctx context.Context, endpointURL string) error
}

// EgressGuardFunc adapts a plain function to EgressGuard.
type EgressGuardFunc func(ctx context.Context, endpointURL string) error

// AuthorizeCredentialEndpoint calls f.
func (f EgressGuardFunc) AuthorizeCredentialEndpoint(ctx context.Context, endpointURL string) error {
	return f(ctx, endpointURL)
}

type egressGuardContextKey struct{}

// WithEgressGuard returns a context carrying guard, which every credential
// call made under that context must satisfy. A nil guard clears any guard the
// parent context carried, which is the permissive default.
//
// This is the per-execution counterpart of SetHTTPClient: that installs the
// client credential calls travel through, this installs who gets to say
// whether a given call happens at all.
func WithEgressGuard(ctx context.Context, guard EgressGuard) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if guard == nil {
		return context.WithValue(ctx, egressGuardContextKey{}, nil)
	}
	return context.WithValue(ctx, egressGuardContextKey{}, guard)
}

// egressGuardFrom returns the guard ctx carries, or nil when it carries none.
func egressGuardFrom(ctx context.Context) EgressGuard {
	if ctx == nil {
		return nil
	}
	guard, _ := ctx.Value(egressGuardContextKey{}).(EgressGuard)
	return guard
}

// authorizeCredentialRequest asks the context's guard about req before it is
// sent. With no guard it is a no-op, so the UI path pays one interface-nil
// comparison and nothing else.
func authorizeCredentialRequest(ctx context.Context, req *http.Request) error {
	guard := egressGuardFrom(ctx)
	if guard == nil || req == nil || req.URL == nil {
		return nil
	}
	return guard.AuthorizeCredentialEndpoint(ctx, req.URL.String())
}

// CredentialProcessRefusedError reports that a profile in the resolution chain
// uses credential_process and the run was not allowed to spawn it.
//
// It names the profile that actually carries the directive, which is not
// necessarily the profile the request selected: a source_profile chain refuses
// at whichever link reaches credential_process.
type CredentialProcessRefusedError struct {
	Profile string
}

func (e *CredentialProcessRefusedError) Error() string {
	return fmt.Sprintf(
		"AWS profile %q uses credential_process, which runs an external program. "+
			"Agent-initiated runs cannot use it. Run this request in the LiteAPI app, "+
			"or switch the profile to static keys or SSO.",
		e.Profile,
	)
}

// CredentialEndpointOrigins reports the STS, SSO and SSO-OIDC endpoint origins
// that resolving auth would contact, in the order it would contact them, with
// duplicates removed. Origins are canonical `scheme://host[:port]` strings.
//
// It performs no network or subprocess work: it reads the same shared config
// and credentials files resolution reads and walks the same branch structure,
// so a caller can pre-compute the destination set a run is allowed to reach.
// resolve expands template variables exactly as it does for Sign; a nil
// resolve is treated as the identity.
//
// A configuration that contacts nothing — static keys, environment
// credentials, credential_process, or no profile at all — yields nil. Files
// that cannot be read are treated as absent, matching resolution, which means
// the answer can be an under-count only in cases where resolution itself would
// fail before reaching the network.
func CredentialEndpointOrigins(auth types.AWSV4Auth, resolve func(string) string) []string {
	if resolve == nil {
		resolve = func(value string) string { return value }
	}
	profileName := strings.TrimSpace(resolve(auth.ProfileName))
	if profileName == "" {
		return nil
	}
	configSections, credentialSections, err := awsSharedConfigSections()
	if err != nil {
		return nil
	}
	var origins []string
	collectCredentialEndpointOrigins(profileName, configSections, credentialSections, map[string]bool{}, &origins)
	if len(origins) == 0 {
		return nil
	}
	return origins
}

// collectCredentialEndpointOrigins mirrors resolveAWSV4ProfileCredentials
// branch for branch. The two must stay in step; the parity test in
// guard_test.go compares this against the URLs the guard actually sees.
func collectCredentialEndpointOrigins(profileName string, configSections, credentialSections map[string]map[string]string, seen map[string]bool, origins *[]string) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" || seen[profileName] {
		return
	}
	seen[profileName] = true
	defer delete(seen, profileName)

	profile := awsV4ProfileValues(profileName, configSections, credentialSections)
	if strings.TrimSpace(profile["web_identity_token_file"]) != "" {
		appendCredentialEndpointOrigin(origins, awsV4STSEndpoint(profile, awsV4STSRegion(profile)))
		return
	}
	if strings.TrimSpace(profile["role_arn"]) != "" {
		// The source profile is resolved first, and its own endpoints are
		// contacted before the AssumeRole that consumes its credentials.
		collectCredentialEndpointOrigins(strings.TrimSpace(profile["source_profile"]), configSections, credentialSections, seen, origins)
		appendCredentialEndpointOrigin(origins, awsV4STSEndpoint(profile, awsV4STSRegion(profile)))
		return
	}
	if awsV4ProfileUsesSSO(profile) {
		ssoProfile := awsV4SSOProfileValues(profile, configSections)
		// A token refresh is only ever attempted for an sso-session profile,
		// so a legacy sso_start_url profile never reaches OIDC.
		if ssoProfile.Session != "" {
			appendCredentialEndpointOrigin(origins, awsV4SSOOIDCEndpoint(profile, ssoProfile.Region))
		}
		appendCredentialEndpointOrigin(origins, awsV4SSOEndpoint(profile, ssoProfile.Region))
	}
}

func appendCredentialEndpointOrigin(origins *[]string, endpoint string) {
	origin := credentialEndpointOrigin(endpoint)
	if origin == "" {
		return
	}
	for _, existing := range *origins {
		if existing == origin {
			return
		}
	}
	*origins = append(*origins, origin)
}

// credentialEndpointOrigin reduces an endpoint to scheme://host[:port].
//
// An endpoint that will not parse is returned trimmed rather than dropped: a
// value this cannot make sense of is one the caller must still be told about,
// and an origin nothing matches denies rather than silently permits.
func credentialEndpointOrigin(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return trimmed
	}
	return parsed.Scheme + "://" + parsed.Host
}
