package routecontract

import (
	"reflect"
	"strings"
)

type healthModel struct {
	Project        map[string]any `json:"project"`
	Status         string         `json:"status"`
	PendingRestart bool           `json:"pending_restart"`
	RestartReason  []string       `json:"restart_reason"`
	Maintenance    map[string]any `json:"maintenance"`
	Version        string         `json:"version"`
	GoVersion      string         `json:"go_version"`
	Build          map[string]any `json:"build"`
	Uptime         string         `json:"uptime"`
	Mode           string         `json:"mode"`
	Timestamp      string         `json:"timestamp"`
	Features       map[string]any `json:"features"`
	Checks         map[string]any `json:"checks"`
	Stats          map[string]any `json:"stats"`
}

type cliAutodiscoverModel struct {
	Name          string         `json:"name"`
	Version       string         `json:"version"`
	API           map[string]any `json:"api"`
	URLs          []string       `json:"urls"`
	Onion         string         `json:"onion,omitempty"`
	I2P           string         `json:"i2p,omitempty"`
	CLIVersions   map[string]any `json:"cli_versions"`
	CLIMinVersion string         `json:"cli_min_version"`
}

type linkModel struct {
	ShortCode      string  `json:"short_code"`
	ShortURL       string  `json:"short_url"`
	DestinationURL string  `json:"destination_url"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      *string `json:"expires_at"`
	ClickCount     int64   `json:"click_count"`
}

type createLinkModel struct {
	ShortCode      string  `json:"short_code"`
	ShortURL       string  `json:"short_url"`
	DestinationURL string  `json:"destination_url"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      *string `json:"expires_at"`
	ClickCount     int64   `json:"click_count"`
	OwnerToken     string  `json:"owner_token,omitempty"`
}

type paginationModel struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
	Pages int   `json:"pages"`
}

type linkListModel struct {
	Data       []linkModel     `json:"data"`
	Pagination paginationModel `json:"pagination"`
}

type linkStatsModel struct {
	ShortCode   string           `json:"short_code"`
	TotalClicks int64            `json:"total_clicks"`
	Referrers   map[string]int   `json:"referrers"`
	TimeSeries  []map[string]any `json:"time_series"`
	Recent      []map[string]any `json:"recent"`
}

type linkCreateRequestModel struct {
	URL       string  `json:"url"`
	Slug      string  `json:"slug,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

type linkUpdateRequestModel struct {
	URL       string  `json:"url,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

type preferenceSnapshotModel struct {
	Theme string            `json:"theme"`
	Lang  string            `json:"lang"`
	Prefs map[string]string `json:"prefs"`
	URL   string            `json:"url"`
	Code  string            `json:"code"`
}

type preferenceUpdateModel struct {
	Theme   string            `json:"theme"`
	Lang    string            `json:"lang"`
	Prefs   map[string]string `json:"prefs"`
	Applied []string          `json:"applied"`
	Ignored []string          `json:"ignored"`
}

type helpPageModel struct {
	Title   string           `json:"title"`
	Content string           `json:"content"`
	Format  string           `json:"format"`
	Pages   []map[string]any `json:"pages"`
}

type tokenCardModel struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Scope       string  `json:"scope"`
	TokenMasked string  `json:"token_masked"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at"`
	ExpiresAt   *string `json:"expires_at"`
	Status      string  `json:"status"`
}

type tokenCollectionModel struct {
	Primary   tokenCardModel   `json:"primary"`
	Linked    []tokenCardModel `json:"linked"`
	MaxLinked int              `json:"max_linked"`
}

type linkedTokenValueModel struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Scope       string  `json:"scope"`
	TokenMasked string  `json:"token_masked"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at"`
	ExpiresAt   *string `json:"expires_at"`
	Status      string  `json:"status"`
	LinkedToken string  `json:"linked_token"`
}

type primaryTokenValueModel struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Scope       string  `json:"scope"`
	TokenMasked string  `json:"token_masked"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at"`
	ExpiresAt   *string `json:"expires_at"`
	Status      string  `json:"status"`
	OwnerToken  string  `json:"owner_token"`
}

type revokedTokenModel struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type primaryTokenIssueModel struct {
	OwnerToken string         `json:"owner_token"`
	Token      tokenCardModel `json:"token"`
}

type primaryTokenRevocationModel struct {
	Tokens    int `json:"tokens"`
	Resources int `json:"resources"`
}

type authorizationModel struct {
	Kind string `json:"kind"`
}

type securityPolicyModel struct {
	Policy string `json:"policy"`
}

type dpoModel struct {
	Name       string `json:"name"`
	Email      string `json:"email"`
	PrivacyURL string `json:"privacy_url"`
}

type updateLinkDataModel struct {
	ID      string    `json:"id"`
	Message string    `json:"message"`
	Link    linkModel `json:"link"`
}

type deleteLinkDataModel struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type graphqlModel struct {
	Data   map[string]any   `json:"data"`
	Errors []map[string]any `json:"errors,omitempty"`
}

type graphqlRequestModel struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName,omitempty"`
	Variables     map[string]any `json:"variables,omitempty"`
}

type openAPIDocumentModel struct {
	OpenAPI    string           `json:"openapi"`
	Info       map[string]any   `json:"info"`
	Servers    []map[string]any `json:"servers"`
	Paths      map[string]any   `json:"paths"`
	Components map[string]any   `json:"components"`
}

type reportPayloadModel struct {
	CSPReport map[string]any `json:"csp-report,omitempty"`
	Type      string         `json:"type,omitempty"`
	URL       string         `json:"url,omitempty"`
	Body      map[string]any `json:"body,omitempty"`
}

type preferencesRequestModel struct {
	Theme string            `json:"theme,omitempty"`
	Lang  string            `json:"lang,omitempty"`
	Prefs map[string]string `json:"prefs,omitempty"`
}

type tokenCreateRequestModel struct {
	Name      string `json:"name,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Expires   string `json:"expires,omitempty"`
	ExpiresOn string `json:"expires_on,omitempty"`
}

type tokenUpdateRequestModel struct {
	Name      string `json:"name,omitempty"`
	Expires   string `json:"expires,omitempty"`
	ExpiresOn string `json:"expires_on,omitempty"`
}

// ModelForName returns the neutral, JSON-tagged model corresponding to a
// named route schema. The API handlers can share these types without making
// routecontract depend on the server package.
func ModelForName(name string) any {
	switch name {
	case "Health":
		return healthModel{}
	case "CLIAutodiscover":
		return cliAutodiscoverModel{}
	case "PreferenceSnapshot", "PreferenceSnapshotResponse", "PreferenceImportResponse":
		return preferenceSnapshotModel{}
	case "PreferenceUpdate", "PreferenceUpdateResponse":
		return preferenceUpdateModel{}
	case "HelpPage", "HelpPageResponse":
		return helpPageModel{}
	case "TokenCard", "TokenCardResponse":
		return tokenCardModel{}
	case "TokenCollection", "TokenCollectionResponse":
		return tokenCollectionModel{}
	case "LinkedTokenValue", "LinkedTokenValueResponse":
		return linkedTokenValueModel{}
	case "PrimaryTokenValue", "PrimaryTokenValueResponse":
		return primaryTokenValueModel{}
	case "RevokedToken", "RevokedTokenResponse":
		return revokedTokenModel{}
	case "PrimaryTokenIssueResponse":
		return primaryTokenIssueModel{}
	case "PrimaryTokenRevocationResponse":
		return primaryTokenRevocationModel{}
	case "AuthorizationResponse":
		return authorizationModel{}
	case "SecurityPolicyResponse":
		return securityPolicyModel{}
	case "DPOResponse":
		return dpoModel{}
	case "UpdateLinkResponse":
		return updateLinkDataModel{}
	case "DeleteLinkResponse":
		return deleteLinkDataModel{}
	case "Link", "GetLinkResponse":
		return linkModel{}
	case "CreateLinkData", "CreateLinkResponse":
		return createLinkModel{}
	case "LinkList":
		return linkListModel{}
	case "LinkStats", "LinkStatsResponse":
		return linkStatsModel{}
	case "CreateLinkRequest":
		return linkCreateRequestModel{}
	case "UpdateLinkRequest":
		return linkUpdateRequestModel{}
	case "PreferencesRequest":
		return preferencesRequestModel{}
	case "TokenCreateRequest":
		return tokenCreateRequestModel{}
	case "TokenUpdateRequest":
		return tokenUpdateRequestModel{}
	case "GraphQLRequest":
		return graphqlRequestModel{}
	case "GraphQLResponse":
		return graphqlModel{}
	case "OpenAPIDocument":
		return openAPIDocumentModel{}
	case "ReportPayload":
		return reportPayloadModel{}
	default:
		return nil
	}
}

// FieldsForModel returns serialized JSON field order for a named model.
// Models represented by a generic envelope or an opaque map return no
// fields, leaving their handler's encoded JSON order authoritative.
func FieldsForModel(name string) []string {
	return FieldsForValue(ModelForName(name))
}

// FieldsForValue returns declared JSON field order from a model value,
// recursively flattening embedded structs the same way encoding/json does.
func FieldsForValue(value any) []string {
	if value == nil {
		return nil
	}
	typeOf := reflect.TypeOf(value)
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	if typeOf.Kind() != reflect.Struct {
		return nil
	}
	return jsonFields(typeOf)
}

func jsonFields(typeOf reflect.Type) []string {
	fields := make([]string, 0, typeOf.NumField())
	for i := 0; i < typeOf.NumField(); i++ {
		field := typeOf.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			fields = append(fields, jsonFields(field.Type)...)
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields = append(fields, name)
	}
	return fields
}
