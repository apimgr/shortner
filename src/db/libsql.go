package db

import (
	"database/sql"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/apimgr/shortner/src/config"
	"github.com/tursodatabase/libsql-client-go/libsql"
)

// openLibSQL opens independent read and write pools for a remote libSQL URL.
// Credentials are removed from the URL before constructing the driver, and
// driver errors are deliberately hidden because they may contain connection
// details.
func openLibSQL(rawURL string, pool Pool, separateAuthToken string) (*Store, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" {
		return nil, errors.New("db: invalid database connection string")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "libsql" && scheme != "https" && scheme != "http" {
		return nil, errors.New("db: unsupported database connection scheme")
	}
	if u.Fragment != "" || strings.ContainsAny(u.Host, "\r\n") {
		return nil, errors.New("db: invalid database connection string")
	}
	if scheme == "http" && !privateRemoteHost(u.Hostname()) {
		return nil, errors.New("db: insecure libSQL URL requires a loopback or private host")
	}

	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("db: invalid database connection options")
	}
	var token string
	urlHasToken := false
	for _, key := range []string{"authToken", "auth_token", "jwt"} {
		values, ok := query[key]
		if !ok {
			continue
		}
		if len(values) != 1 || values[0] == "" || token != "" {
			return nil, errors.New("db: invalid database authentication options")
		}
		token = values[0]
		urlHasToken = true
		query.Del(key)
	}
	if !urlHasToken {
		token = separateAuthToken
	}
	tls := scheme != "http"
	if values, ok := query["tls"]; ok {
		requested, parseErr := parseLibSQLTLS(scheme, values, tls)
		if parseErr != nil {
			return nil, parseErr
		}
		tls = requested
		query.Del("tls")
	}
	if len(query) != 0 {
		return nil, errors.New("db: unsupported database connection option")
	}
	u.RawQuery = ""
	u.ForceQuery = false

	options := make([]libsql.Option, 0, 2)
	if token != "" {
		options = append(options, libsql.WithAuthToken(token))
	}
	options = append(options, libsql.WithTls(tls))
	connector, err := libsql.NewConnector(u.String(), options...)
	if err != nil {
		return nil, errors.New("db: invalid database connection string")
	}

	readers := pool.MaxOpen
	if readers < 1 {
		readers = 5
	}
	w := sql.OpenDB(connector)
	r := sql.OpenDB(connector)
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	r.SetMaxOpenConns(readers)
	r.SetMaxIdleConns(pool.MaxIdle)
	if pool.MaxIdleTime > 0 {
		w.SetConnMaxIdleTime(pool.MaxIdleTime)
		r.SetConnMaxIdleTime(pool.MaxIdleTime)
	}
	if pool.MaxLifetime > 0 {
		w.SetConnMaxLifetime(pool.MaxLifetime)
		r.SetConnMaxLifetime(pool.MaxLifetime)
	}
	store := &Store{w: w, r: r}
	if err := w.Ping(); err != nil {
		_ = store.Close()
		return nil, errors.New("db: database connection failed")
	}
	return store, nil
}

// parseLibSQLTLS parses the configured TLS query option with the shared
// truthy/falsy values while enforcing the URL scheme's transport policy.
func parseLibSQLTLS(scheme string, values []string, defaultTLS bool) (bool, error) {
	if len(values) != 1 {
		return false, errors.New("db: invalid database TLS option")
	}
	requested, err := config.ParseBool(values[0], defaultTLS)
	if err != nil || (scheme != "libsql" && requested != defaultTLS) {
		return false, errors.New("db: invalid database TLS option")
	}
	if scheme == "libsql" && !requested {
		return false, errors.New("db: libsql URLs require TLS")
	}
	return requested, nil
}

func privateRemoteHost(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	addresses, err := net.LookupIP(host)
	if err != nil || len(addresses) == 0 {
		return false
	}
	for _, ip := range addresses {
		if !ip.IsLoopback() && !ip.IsPrivate() {
			return false
		}
	}
	return true
}
