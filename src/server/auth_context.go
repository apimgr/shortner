package server

import (
	"context"
	"github.com/apimgr/shortner/src/db"
)

type authOwnerContextKey struct{}
type authTokenContextKey struct{}

func tokenFromContext(ctx context.Context) *db.APIToken {
	value, _ := ctx.Value(authTokenContextKey{}).(*db.APIToken)
	return value
}
