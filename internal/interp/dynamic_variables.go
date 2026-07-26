package interp

// US-050 — Postman dynamic variables.
//
// Imported Postman collections lean on {{$randomInt}}, {{$guid}} and friends.
// Before this, only {{$timestamp}} and {{$isoTimestamp}} resolved, so every
// other placeholder went out on the wire literally — a request that "worked" in
// Postman would send the string "{{$randomEmail}}" as an email address.
//
// TWO RULES THE STORY IS RIGHT TO SPELL OUT, because both are easy to get
// wrong in the direction that looks fine:
//
//   1. Each occurrence resolves INDEPENDENTLY. strings.ReplaceAll with one
//      generated value — which is how the existing $timestamp works, correctly,
//      since a single request should carry one timestamp — would give every
//      {{$randomInt}} in a body the same number. A user generating ten distinct
//      records would get ten identical ones and no error.
//
//   2. An unknown $ variable is left LITERAL, not resolved to empty. Silently
//      emptying it turns a typo like {{$randomEmial}} into a request with a
//      blank field that looks deliberate. Left literal, the user sees their own
//      typo in the outgoing request.

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// dynamicVariablePattern matches a {{$name}} placeholder. Deliberately narrow:
// only $-prefixed names, so ordinary {{var}} interpolation is untouched.
var dynamicVariablePattern = regexp.MustCompile(`\{\{\$([A-Za-z][A-Za-z0-9_]*)\}\}`)

// randomIntBelow returns a uniform value in [0,n). crypto/rand rather than
// math/rand: these values end up in request bodies, and a user generating test
// credentials or IDs should not get a sequence an observer can predict from one
// sample. The cost is irrelevant next to the HTTP request that follows.
func randomIntBelow(n int64) int64 {
	if n <= 0 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		// A failing system CSPRNG is not something this can paper over
		// meaningfully; falling back to the clock keeps the request moving
		// rather than failing it, which matches how the rest of interpolation
		// degrades.
		return time.Now().UnixNano() % n
	}
	return v.Int64()
}

func randomFrom(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[randomIntBelow(int64(len(values)))]
}

// Word lists are small on purpose. They exist so an imported collection
// produces plausible-looking values, not to be a faker library — a large corpus
// would be dead weight in a desktop binary for no added correctness.
var (
	dynamicFirstNames  = []string{"Ada", "Grace", "Alan", "Edsger", "Barbara", "Ken", "Katherine", "Linus", "Margaret", "Dennis"}
	dynamicLastNames   = []string{"Lovelace", "Hopper", "Turing", "Dijkstra", "Liskov", "Thompson", "Johnson", "Torvalds", "Hamilton", "Ritchie"}
	dynamicCities      = []string{"London", "Lagos", "Osaka", "Toronto", "Lisbon", "Nairobi", "Melbourne", "Helsinki", "Bogota", "Karachi"}
	dynamicCountries   = []string{"Portugal", "Kenya", "Japan", "Canada", "Finland", "Nigeria", "Australia", "Colombia", "Pakistan", "Ireland"}
	dynamicStreets     = []string{"Maple Street", "Harbour Road", "Elm Avenue", "Station Lane", "Riverside Way", "Oak Terrace", "Kings Road"}
	dynamicCompanies   = []string{"Northwind", "Acme Systems", "Globex", "Initech", "Umbrella Labs", "Soylent Works", "Hooli"}
	dynamicJobTitles   = []string{"Engineer", "Analyst", "Designer", "Architect", "Technician", "Consultant", "Researcher"}
	dynamicColors      = []string{"red", "green", "blue", "cyan", "magenta", "yellow", "black", "white", "orange", "purple"}
	dynamicDomainWords = []string{"example", "testsite", "demoapp", "sandbox", "proving", "staging"}
	dynamicTLDs        = []string{"com", "org", "net", "io", "dev"}
)

func randomUUIDString() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%08x-0000-4000-8000-%012x", time.Now().Unix(), time.Now().UnixNano()&0xFFFFFFFFFFFF)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func randomDomainNameString() string {
	return fmt.Sprintf("%s.%s", randomFrom(dynamicDomainWords), randomFrom(dynamicTLDs))
}

// resolveDynamicVariable returns the value for a $-name and whether it is one
// this build knows. The bool is what implements rule 2: an unknown name is
// reported, not guessed at.
func resolveDynamicVariable(name string) (string, bool) {
	switch name {
	case "timestamp":
		return fmt.Sprintf("%d", time.Now().Unix()), true
	case "isoTimestamp":
		return time.Now().UTC().Format(time.RFC3339), true
	case "guid", "randomUUID":
		return randomUUIDString(), true
	case "randomInt":
		// Postman's $randomInt is 0-1000 inclusive.
		return fmt.Sprintf("%d", randomIntBelow(1001)), true
	case "randomFirstName":
		return randomFrom(dynamicFirstNames), true
	case "randomLastName":
		return randomFrom(dynamicLastNames), true
	case "randomFullName":
		return randomFrom(dynamicFirstNames) + " " + randomFrom(dynamicLastNames), true
	case "randomUserName":
		return strings.ToLower(randomFrom(dynamicFirstNames)) + fmt.Sprintf("%d", randomIntBelow(1000)), true
	case "randomEmail":
		return fmt.Sprintf("%s.%s@%s",
			strings.ToLower(randomFrom(dynamicFirstNames)),
			strings.ToLower(randomFrom(dynamicLastNames)),
			randomDomainNameString()), true
	case "randomIP":
		return fmt.Sprintf("%d.%d.%d.%d", randomIntBelow(256), randomIntBelow(256), randomIntBelow(256), randomIntBelow(256)), true
	case "randomPhoneNumber":
		return fmt.Sprintf("%03d-%03d-%04d", randomIntBelow(1000), randomIntBelow(1000), randomIntBelow(10000)), true
	case "randomCity":
		return randomFrom(dynamicCities), true
	case "randomCountry":
		return randomFrom(dynamicCountries), true
	case "randomStreetAddress":
		return fmt.Sprintf("%d %s", 1+randomIntBelow(999), randomFrom(dynamicStreets)), true
	case "randomCompanyName":
		return randomFrom(dynamicCompanies), true
	case "randomJobTitle":
		return randomFrom(dynamicJobTitles), true
	case "randomPassword":
		const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		out := make([]byte, 12)
		for i := range out {
			out[i] = alphabet[randomIntBelow(int64(len(alphabet)))]
		}
		return string(out), true
	case "randomUrl":
		return "https://" + randomDomainNameString(), true
	case "randomDomainName":
		return randomDomainNameString(), true
	case "randomColor":
		return randomFrom(dynamicColors), true
	case "randomBoolean":
		if randomIntBelow(2) == 0 {
			return "false", true
		}
		return "true", true
	default:
		return "", false
	}
}

// interpolateDynamicVariables replaces every known {{$name}} occurrence.
//
// ReplaceAllStringFunc, not ReplaceAll: the callback runs once per match, which
// is what makes each occurrence independent. Unknown names are returned
// unchanged, so they travel to the server as written and the user can see their
// own typo.
func InterpolateDynamicVariables(input string) string {
	if !strings.Contains(input, "{{$") {
		return input
	}
	return dynamicVariablePattern.ReplaceAllStringFunc(input, func(match string) string {
		name := match[3 : len(match)-2]
		if value, ok := resolveDynamicVariable(name); ok {
			return value
		}
		return match
	})
}
