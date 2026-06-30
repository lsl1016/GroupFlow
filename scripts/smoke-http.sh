#!/usr/bin/env bash
set -euo pipefail
BASE=${BASE:-http://localhost}
login() {
  curl -sS "$BASE/api/v1/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$1\"}"
}
TOKEN=$(login user_001 | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
echo "token=${TOKEN:0:24}..."
curl -sS "$BASE/api/v1/groups" -H "Authorization: Bearer $TOKEN" | jq .
