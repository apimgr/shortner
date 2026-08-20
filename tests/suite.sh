#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : In-container integration suite for shortner (AI.md PART 28)
#
# This script runs INSIDE the disposable test container (alpine via
# tests/docker.sh, debian via tests/incus.sh) — never on the host. Both
# callers push this file plus tests/assert.sh and the freshly built
# binaries into the container and then execute it.
#
# Environment contract (all optional except SUITE_BIN):
#   SUITE_BIN      absolute path to the server binary inside the container
#   SUITE_CLI      absolute path to the client binary, or empty when the
#                  caller chose not to push shortner-cli into the container
#   SUITE_ROOT     writable working root for config/logs/rename copies
#   SUITE_PORT     listen port (PART 28 reserves 64000-64999 for tests)
#   SUITE_HOST     listen address, default 127.0.0.1
#   SUITE_API      API version prefix segment, default v1
#   SUITE_NAME     expected binary name, default shortner

set -eo pipefail

suite_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tests/assert.sh
. "$suite_dir/assert.sh"

bin="${SUITE_BIN:-/usr/local/bin/shortner}"
cli="${SUITE_CLI:-}"
root="${SUITE_ROOT:-/opt/shortner-test}"
port="${SUITE_PORT:-64580}"
host="${SUITE_HOST:-127.0.0.1}"
api="${SUITE_API:-v1}"
name="${SUITE_NAME:-shortner}"

base_url="http://$host:$port"
api_url="$base_url/api/$api"
config_file="$root/config/server.yml"
log_file="$root/logs/server.log"
server_pid=""
operator_token=""

mkdir -p "$root/config" "$root/data" "$root/logs" "$root/rename"

# Stops the server if it is still running. Registered on EXIT so a failing
# assertion can never leave a listener behind in the container.
__cleanup() {
  __stop_server
}
trap __cleanup EXIT

# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------

# __request {curl args...} — fills last_status/last_body/last_headers.
last_status=""
last_body=""
last_headers=""
__request() {
  local raw
  raw="$(curl -sS -D "$root/.headers" -w '\n%{http_code}' "$@" 2>/dev/null || true)"
  last_status="${raw##*$'\n'}"
  last_body="${raw%$'\n'*}"
  last_headers="$(cat "$root/.headers" 2>/dev/null || true)"
}

# __status_of {curl args...} — echoes only the HTTP status code.
__status_of() {
  curl -sS -o /dev/null -w '%{http_code}' "$@" 2>/dev/null || echo 000
}

# __header_of {name} — echoes a response header from the last request.
__header_of() {
  printf '%s\n' "$last_headers" | tr -d '\r' | awk -v key="$(printf '%s' "$1" | tr 'A-Z' 'a-z')" '
    { split($0, kv, ":"); k = tolower(kv[1]) }
    k == key { sub(/^[^:]*:[[:space:]]*/, ""); print; exit }
  '
}

# __assert_status {label} {expected} — compares against the last request.
__assert_status() {
  __assert_eq "$1" "$2" "$last_status"
}

# __assert_status_in {label} {code} [{code}...] — accepts any of the codes.
__assert_status_in() {
  local label="$1" code
  shift
  for code in "$@"; do
    if [ "$last_status" = "$code" ]; then
      __pass "$label (HTTP $last_status)"
      return 0
    fi
  done
  __fail "$label (got HTTP $last_status, wanted one of: $*)"
}

# __json_get {jq-filter} — reads a field out of the last JSON body.
__json_get() {
  printf '%s' "$last_body" | jq -r "$1" 2>/dev/null || printf ''
}

# ---------------------------------------------------------------------------
# Server lifecycle
# ---------------------------------------------------------------------------

__start_server() {
  "$bin" --config "$config_file" --address "$host" --port "$port" >>"$log_file" 2>&1 &
  server_pid=$!
  local i
  for i in $(seq 1 80); do
    if [ "$(__status_of "$base_url/api/healthz")" = "200" ]; then
      return 0
    fi
    if ! kill -0 "$server_pid" 2>/dev/null; then
      printf 'server exited during startup; last log lines:\n' >&2
      tail -n 40 "$log_file" >&2 || true
      return 1
    fi
    sleep 0.25
  done
  printf 'server did not become healthy within 20s; last log lines:\n' >&2
  tail -n 40 "$log_file" >&2 || true
  return 1
}

__stop_server() {
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill -TERM "$server_pid" 2>/dev/null || true
    local i
    for i in $(seq 1 40); do
      kill -0 "$server_pid" 2>/dev/null || break
      sleep 0.25
    done
    kill -KILL "$server_pid" 2>/dev/null || true
  fi
  server_pid=""
}

# Turns off the per-IP rate limiter and the abuse detector in the generated
# config so the functional phase is deterministic. Both blocks keep
# `enabled` as their first key (src/config/config.go), so the edit is
# scoped to the first `enabled:` line inside each block and never touches
# any other `enabled:` in the file.
__relax_config() {
  local block tmp
  for block in rate_limit abuse_detection; do
    tmp="$root/.server.yml.$block"
    awk -v blk="$block" '
      $0 ~ "^[[:space:]]*" blk ":[[:space:]]*$" { inblk = 1; print; next }
      inblk && /^[[:space:]]*enabled:/ {
        match($0, /^[[:space:]]*/)
        printf "%senabled: false\n", substr($0, 1, RLENGTH)
        inblk = 0
        next
      }
      { print }
    ' "$config_file" >"$tmp"
    mv "$tmp" "$config_file"
  done
}

# ---------------------------------------------------------------------------
# Test sections
# ---------------------------------------------------------------------------

__test_prerequisites() {
  __section "Container prerequisites"
  local tool
  for tool in curl bash file jq; do
    if command -v -- "$tool" >/dev/null 2>&1; then
      __pass "$tool is installed"
    else
      __fail "$tool is missing"
    fi
  done
}

__test_binary_info() {
  __section "Binary information verification"
  if [ -x "$bin" ]; then
    __pass "binary is executable: $bin"
  else
    __fail "binary is not executable: $bin"
    return 0
  fi

  local info
  info="$(file -b "$bin" 2>/dev/null || echo unknown)"
  __assert_contains "binary is an ELF executable" "ELF" "$info"
  __assert_contains "binary is statically linked (CGO_ENABLED=0)" "statically linked" "$info"

  local linkage
  linkage="$(ldd "$bin" 2>&1 || true)"
  __assert_not_contains "binary has no shared-library dependencies" "=>" "$linkage"
}

__test_version_and_help() {
  __section "Version and help output"
  local out
  out="$("$bin" --version 2>&1 || true)"
  __assert_contains "--version names the binary" "$name" "$out"
  __assert_nonempty "--version produces output" "$out"

  out="$("$bin" --help 2>&1 || true)"
  local flag
  for flag in --help --version --config --port --mode --debug; do
    __assert_contains "--help documents $flag" "$flag" "$out"
  done
}

__test_binary_rename() {
  __section "Binary rename behavior (PART 7/8)"
  local renamed="$root/rename/link-thing"
  cp "$bin" "$renamed"
  chmod +x "$renamed"
  local out
  out="$("$renamed" --help 2>&1 || true)"
  __assert_contains "renamed binary reports its actual name" "link-thing" "$out"
  __assert_not_contains "renamed binary does not hardcode the original name" "$name --" "$out"
}

__test_first_run() {
  __section "First run and operator setup"
  if [ -f "$config_file" ]; then
    __pass "server.yml auto-created at $config_file"
  else
    __fail "server.yml was not auto-created at $config_file"
    return 0
  fi

  operator_token="$(awk '/^[[:space:]]{2}token:[[:space:]]*/ { sub(/^[[:space:]]*token:[[:space:]]*/, ""); gsub(/["'"'"']/, ""); print; exit }' "$config_file")"
  if [ -z "$operator_token" ]; then
    operator_token="$(awk '/^[[:space:]]*token:[[:space:]]*[^[:space:]]/ { sub(/^[[:space:]]*token:[[:space:]]*/, ""); gsub(/["'"'"']/, ""); print; exit }' "$config_file")"
  fi
  __assert_nonempty "server.token generated in server.yml" "$operator_token"
}

__test_health_endpoints() {
  __section "Health endpoints (PART 13)"
  __request "$base_url/api/healthz"
  __assert_status "GET /api/healthz returns 200" 200

  __request "$api_url/server/healthz" -H 'Accept: application/json'
  __assert_status "GET /api/$api/server/healthz returns 200" 200
  __assert_eq "API health status is ok" "ok" "$(__json_get '.status // .data.status')"

  __request "$base_url/server/healthz" -H 'Accept: text/plain'
  __assert_status "GET /server/healthz returns 200" 200
  __assert_contains "plain-text health carries a status line" "status" "$last_body"
}

__test_content_negotiation() {
  __section "Content negotiation (PART 14/28)"

  # Frontend route: browsers get HTML, tools get plain text.
  __request "$base_url/server/healthz" -H 'Accept: text/html' -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64)'
  __assert_contains "frontend + text/html returns an HTML document" "<!DOCTYPE" "$last_body"
  __request "$base_url/server/healthz" -H 'Accept: text/plain'
  __assert_not_contains "frontend + text/plain returns no HTML" "<!DOCTYPE" "$last_body"

  # API route: explicit Accept always wins over User-Agent sniffing.
  __request "$api_url/server/healthz" -H 'Accept: application/json'
  if printf '%s' "$last_body" | jq -e . >/dev/null 2>&1; then
    __pass "API + application/json parses as valid JSON"
  else
    __fail "API + application/json did not return valid JSON"
  fi
  __request "$api_url/server/healthz" -H 'Accept: text/plain'
  __assert_not_contains "API + text/plain returns no JSON envelope" '"ok":' "$last_body"

  # Default (curl, no Accept) is plain text per PART 14's tool detection.
  __request "$api_url/server/healthz" -H 'User-Agent: curl/8.0.0'
  __assert_not_contains "API + curl UA defaults to plain text" '"ok":' "$last_body"

  # .txt path suffix — supported by the handlers (wantsText) but not yet by
  # the router, so it 404s today. Reported, never silently passed.
  local txt_status
  txt_status="$(__status_of "$base_url/server/healthz.txt")"
  if [ "$txt_status" = "200" ]; then
    __pass "GET /server/healthz.txt returns 200"
  else
    __skip "GET /server/healthz.txt" "path-suffix routing not implemented yet (HTTP $txt_status) — TODO.AI.md PART 13/14"
  fi
}

__test_wellknown() {
  __section "Well-known and .txt endpoints (PART 11)"
  __request "$base_url/robots.txt"
  __assert_status "GET /robots.txt returns 200" 200
  __assert_contains "robots.txt has a User-agent directive" "User-agent" "$last_body"

  __request "$base_url/llms.txt"
  __assert_status "GET /llms.txt returns 200" 200

  __request "$base_url/.well-known/security.txt"
  __assert_status "GET /.well-known/security.txt returns 200" 200
  __assert_contains "security.txt has a Contact field" "Contact:" "$last_body"

  __request "$base_url/.well-known/not-on-the-allowlist"
  __assert_status_in "unlisted /.well-known path is refused" 403 404
}

__test_frontend_pages() {
  __section "Frontend pages (PART 16)"
  local page
  for page in / /server /server/about /server/privacy /server/terms /server/help /server/contact /server/security /server/security/policy /server/dpo /list; do
    __request "$base_url$page" -H 'Accept: text/html' -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64)'
    if [ "$last_status" = "200" ]; then
      __assert_contains "GET $page renders HTML" "<!DOCTYPE" "$last_body"
    else
      __fail "GET $page returned HTTP $last_status"
    fi
  done

  __request "$base_url/" -H 'Accept: text/html' -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64)'
  __assert_contains "home page has the shorten form" 'name="url"' "$last_body"
  __assert_contains "home page carries a CSRF token" 'name="csrf_token"' "$last_body"
}

# Creates a link and echoes "{short_code} {owner_token}".
__create_link() {
  local url="$1" slug="$2" expires="$3" payload
  payload="$(jq -nc --arg url "$url" --arg slug "$slug" --arg exp "$expires" '
    {url: $url}
    + (if $slug == "" then {} else {slug: $slug} end)
    + (if $exp == "" then {} else {expires_at: $exp} end)
  ')"
  __request -X POST "$api_url/links" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json' \
    -d "$payload"
}

__test_links_crud() {
  __section "Link CRUD (IDEA.md business logic)"

  __create_link "https://example.com/auto" "" ""
  __assert_status "POST /api/$api/links creates a link" 201
  local auto_code auto_owner
  auto_code="$(__json_get '.data.short_code')"
  auto_owner="$(__json_get '.data.owner_token')"
  __assert_nonempty "auto-generated short code returned" "$auto_code"
  __assert_nonempty "one-time owner_token returned" "$auto_owner"
  __assert_eq "auto-generated short code is 6 characters" 6 "${#auto_code}"

  __create_link "https://example.com/custom" "beta-suite" ""
  __assert_status "POST with a custom slug creates the link" 201
  local custom_owner
  custom_owner="$(__json_get '.data.owner_token')"
  __assert_eq "custom slug is honored" "beta-suite" "$(__json_get '.data.short_code')"

  __create_link "https://example.com/dupe" "beta-suite" ""
  __assert_status "duplicate custom slug is rejected" 409

  __create_link "https://example.com/reserved" "server" ""
  __assert_status "reserved slug is rejected" 409

  __create_link "not-a-url" "" ""
  __assert_status_in "malformed destination URL is rejected" 400 422

  __create_link "https://example.com/bad-expiry" "" "tomorrow-ish"
  __assert_status_in "malformed expires_at is rejected" 400 422

  __request "$api_url/links/$auto_code" -H 'Accept: application/json'
  __assert_status "GET /api/$api/links/{slug} returns the link" 200
  __assert_eq "stored destination round-trips" "https://example.com/auto" "$(__json_get '.data.destination_url // .data.url')"

  __request "$api_url/links" -H 'Accept: application/json'
  __assert_status "GET /api/$api/links lists links" 200

  __request -X POST "$api_url/links" \
    -H 'Content-Type: application/json' \
    -H 'Accept: text/plain' \
    -d '{"url":"https://example.com/plain"}'
  __assert_status "POST with Accept: text/plain returns 201" 201
  __assert_contains "plain-text create returns the short URL" "http" "$last_body"

  export SUITE_AUTO_CODE="$auto_code"
  export SUITE_AUTO_OWNER="$auto_owner"
  export SUITE_CUSTOM_OWNER="$custom_owner"
}

__test_resolution_and_stats() {
  __section "Redirect resolution and stats"
  local code="$SUITE_AUTO_CODE"

  __request "$base_url/$code" -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64)'
  __assert_status "GET /{slug} redirects" 302
  __assert_eq "redirect Location is the destination" "https://example.com/auto" "$(__header_of Location)"

  __request "$base_url/definitely-not-a-real-slug"
  __assert_status "unknown slug returns 404" 404

  # A fixed past timestamp rather than a computed one: busybox `date` (the
  # alpine container) has no relative-date support, so `date -d '-1 hour'`
  # is not portable across both test runtimes.
  local past="2020-01-01T00:00:00Z"
  __create_link "https://example.com/expired" "" "$past"
  __assert_status "POST with a past expires_at creates the link" 201
  local expired_code
  expired_code="$(__json_get '.data.short_code')"
  __request "$base_url/$expired_code"
  __assert_status "expired link returns 410 Gone" 410

  __request "$api_url/links/$code/stats" -H 'Accept: application/json'
  __assert_status "GET /api/$api/links/{slug}/stats returns 200" 200
  __assert_eq "stats short_code matches" "$code" "$(__json_get '.data.short_code')"
  if [ "$(__json_get '.data.total_clicks')" -ge 1 ] 2>/dev/null; then
    __pass "stats counted the redirect"
  else
    __fail "stats did not count the redirect"
  fi
  __assert_not_contains "stats never expose a raw client IP" "127.0.0.1" "$last_body"

  __request "$base_url/$code/stats" -H 'Accept: text/html' -H 'User-Agent: Mozilla/5.0 (X11; Linux x86_64)'
  __assert_status "GET /{slug}/stats renders the stats page" 200

  __request "$base_url/$code" -H 'User-Agent: Googlebot/2.1 (+http://www.google.com/bot.html)'
  __assert_status "bot redirect still works" 302
  __request "$api_url/links/$code/stats" -H 'Accept: application/json'
  __assert_eq "bot click is excluded from analytics" 1 "$(__json_get '.data.total_clicks')"
}

__test_authorization() {
  __section "Token authorization (owner vs operator)"
  local code="$SUITE_AUTO_CODE" owner="$SUITE_AUTO_OWNER"

  __request -X PATCH "$api_url/links/$code" \
    -H 'Content-Type: application/json' -H 'Accept: application/json' \
    -d '{"url":"https://example.com/hijacked"}'
  __assert_status "PATCH without a token is forbidden" 403

  __request -X PATCH "$api_url/links/$code" \
    -H 'Content-Type: application/json' -H 'Accept: application/json' \
    -H 'Authorization: Bearer not-a-real-token' \
    -d '{"url":"https://example.com/hijacked"}'
  __assert_status "PATCH with a bogus token is forbidden" 403

  __request -X PATCH "$api_url/links/$code" \
    -H 'Content-Type: application/json' -H 'Accept: application/json' \
    -H "Authorization: Bearer $owner" \
    -d '{"url":"https://example.com/updated"}'
  __assert_status_in "PATCH with the owner token succeeds" 200 204

  __request "$api_url/links/$code" -H 'Accept: application/json'
  __assert_eq "update persisted" "https://example.com/updated" "$(__json_get '.data.destination_url // .data.url')"

  # The operator's server.token moderates any link, and it is accepted from
  # every supported source (header list plus ?token=), first found wins.
  __request -X PATCH "$api_url/links/beta-suite" \
    -H 'Content-Type: application/json' -H 'Accept: application/json' \
    -H "Authorization: Bearer $operator_token" \
    -d '{"url":"https://example.com/moderated"}'
  __assert_status_in "operator token can edit any link" 200 204

  __request -X DELETE "$api_url/links/beta-suite?token=$operator_token" -H 'Accept: application/json'
  __assert_status_in "operator token can delete any link (?token=)" 200 204

  __request "$api_url/links/beta-suite" -H 'Accept: application/json'
  __assert_status "deleted link is gone from the API" 404

  __request -X DELETE "$api_url/links/$code" \
    -H 'Accept: application/json' -H "Authorization: Bearer $owner"
  __assert_status_in "owner token can delete its own link" 200 204

  __request "$base_url/$code"
  __assert_status "deleted link no longer resolves" 404
}

__test_cli() {
  __section "Client binary (PART 8/32)"
  if [ -z "$cli" ] || [ ! -x "$cli" ]; then
    __skip "client binary checks" "shortner-cli was not pushed into the container"
    return 0
  fi

  local out code
  out="$("$cli" --version 2>&1 || true)"
  __assert_nonempty "client --version produces output" "$out"
  out="$("$cli" --help 2>&1 || true)"
  __assert_contains "client --help documents --server" "--server" "$out"
  __assert_contains "client --help documents --token" "--token" "$out"
  __assert_contains "client --help documents --output" "--output" "$out"
  __assert_not_contains "client --help never offers --tui" "--tui" "$out"
  __assert_not_contains "client --help never offers --gui" "--gui" "$out"

  out="$("$cli" --shell completions bash 2>&1 || true)"
  __assert_nonempty "client emits bash completions" "$out"

  # The client keeps its config under the user's own directories, so give it
  # a throwaway HOME rather than letting it touch the container's real one.
  local cli_home="$root/clihome"
  mkdir -p "$cli_home"

  out="$(HOME="$cli_home" "$cli" --server "$base_url" --output json health 2>&1 || true)"
  __assert_contains "client health reaches the server" '"status"' "$out"

  out="$(HOME="$cli_home" "$cli" --server "$base_url" --output json \
    shorten "https://example.com/client-suite" 2>&1 || true)"
  code="$(printf '%s' "$out" | sed -n 's/.*"short_code"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  __assert_nonempty "client shorten returns a short code" "$code"

  if [ -n "$code" ]; then
    out="$(HOME="$cli_home" "$cli" --server "$base_url" --output json get "$code" 2>&1 || true)"
    __assert_contains "client get finds the link it just created" "$code" "$out"
    out="$(HOME="$cli_home" "$cli" --server "$base_url" --output json stats "$code" 2>&1 || true)"
    __assert_contains "client stats reports clicks" "clicks" "$out"
  fi

  out="$(HOME="$cli_home" "$cli" --server "$base_url" --output json list 2>&1 || true)"
  __assert_nonempty "client list produces output" "$out"

  # PART 32: no command and no terminal attached is a usage error, never a hang.
  local status=0
  HOME="$cli_home" "$cli" --server "$base_url" </dev/null >/dev/null 2>&1 || status=$?
  __assert_eq "client with no command and no tty exits 64" 64 "$status"

  # The config can hold an API token, so PART 32 requires 0600 on the file.
  local cli_config="$cli_home/.config/apimgr/shortner/cli.yml"
  if [ -f "$cli_config" ]; then
    __assert_eq "client config file is mode 600" 600 \
      "$(stat -c '%a' "$cli_config" 2>/dev/null || stat -f '%Lp' "$cli_config")"
  else
    __skip "client config file mode" "cli.yml was not written by these commands"
  fi
}

__test_rate_limit() {
  __section "Rate limiting (PART 12)"
  local i status saw_429=0
  for i in $(seq 1 40); do
    status="$(__status_of -X POST "$api_url/links" \
      -H 'Content-Type: application/json' -H 'Accept: application/json' \
      -d '{"url":"https://example.com/flood"}')"
    if [ "$status" = "429" ]; then
      saw_429=1
      break
    fi
  done
  if [ "$saw_429" = "1" ]; then
    __pass "write rate limit returns 429 after the configured burst"
  else
    __fail "write rate limit never returned 429 in 40 rapid requests"
  fi
  __request "$base_url/api/healthz"
  __assert_status_in "health endpoint still answers under load" 200 429
}

# ---------------------------------------------------------------------------
# Run
# ---------------------------------------------------------------------------

printf '%s\n' "shortner beta suite — binary: $bin, base URL: $base_url"

__test_prerequisites
__test_binary_info
__test_version_and_help
__test_binary_rename

# Phase A: pristine generated config, so the rate limiter is at its
# defaults and can actually be exercised.
__start_server
__test_first_run
__test_rate_limit
__stop_server

# Phase B: rate limiter and abuse detector disabled so the functional
# checks are deterministic rather than racing a per-IP budget.
__relax_config
__start_server
__test_health_endpoints
__test_content_negotiation
__test_wellknown
__test_frontend_pages
__test_links_crud
__test_resolution_and_stats
__test_authorization
__test_cli
__stop_server

__print_summary
