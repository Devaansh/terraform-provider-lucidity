package provider

// knownDeployment pairs a Lucidity Dashboard Login URL with the API Base URL
// it maps to, per the Getting Started guide. This is the single source of
// truth for both the dashboard_login_url validator and the actual base URL
// used to build the API client.
type knownDeployment struct {
	dashboardLoginURL string
	apiBaseURL        string
}

// knownDeployments lists every deployment Lucidity documents. There is no
// free-form override: an undocumented deployment requires a provider update
// to add it here, per the maintainer's choice to keep dashboard_login_url a
// closed, validated set rather than an escape-hatch string.
var knownDeployments = []knownDeployment{
	{"https://www.web.lucidity.dev/dashboard", "https://dash-back.lucidity.dev"},
	{"https://web-azurepls.lucidity.cloud/dashboard", "https://dashboard-azurepls.lucidity.cloud"},
	{"https://app.lucidity.cloud", "https://app.lucidity.cloud"},
	{"https://in.app.lucidity.cloud", "https://in.app.lucidity.cloud"},
	{"https://eu.app.lucidity.cloud", "https://eu.app.lucidity.cloud"},
}

// validDashboardLoginURLs returns the allowed dashboard_login_url values, in
// the order declared above, for use with a OneOf schema validator.
func validDashboardLoginURLs() []string {
	out := make([]string, len(knownDeployments))
	for i, d := range knownDeployments {
		out[i] = d.dashboardLoginURL
	}
	return out
}

// apiBaseURLFor looks up the API base URL for a dashboard login URL. Callers
// can treat a false return as unreachable in practice — the OneOf validator
// built from validDashboardLoginURLs rejects any other value before
// Configure runs — but should still handle it rather than panic, in case the
// validator and this table ever drift apart.
func apiBaseURLFor(dashboardLoginURL string) (string, bool) {
	for _, d := range knownDeployments {
		if d.dashboardLoginURL == dashboardLoginURL {
			return d.apiBaseURL, true
		}
	}
	return "", false
}
