package ingest

import (
	"context"

	"jobassistant/backend/internal/catalog"
)

// SupportsSource identifies implemented adapters, never arbitrary supplied URLs.
func SupportsSource(source string) bool {
	switch source {
	case "tencent-campus", "tencent-internships", "huawei-campus", "huawei-internships", "cmb-campus", zjditSource, chaitinSource:
		return true
	}
	_, moka := mokaConfig(source)
	_, feishu := feishuConfig(source)
	_, workday := workdayConfig(source)
	_, beisen := beisenConfig(source)
	return moka || feishu || workday || beisen
}

// ReadSource returns one complete anonymous snapshot without publishing it.
// Each Reader belongs to a single attempt and must not be used concurrently.
func (r *Reader) ReadSource(ctx context.Context, source string) ([]catalog.Document, int, error) {
	switch source {
	case "tencent-campus":
		return r.Tencent(ctx)
	case "huawei-campus":
		return r.Huawei(ctx)
	case "tencent-internships":
		return r.TencentInternships(ctx)
	case "huawei-internships":
		return r.HuaweiInternships(ctx)
	case "cmb-campus":
		return r.CMB(ctx)
	case zjditSource:
		return r.ZJDIT(ctx)
	case chaitinSource:
		return r.Chaitin(ctx)
	}
	if _, ok := mokaConfig(source); ok {
		return r.Moka(ctx, source)
	}
	if _, ok := feishuConfig(source); ok {
		return r.Feishu(ctx, source)
	}
	if _, ok := workdayConfig(source); ok {
		return r.Workday(ctx, source)
	}
	if _, ok := beisenConfig(source); ok {
		return r.Beisen(ctx, source)
	}
	return nil, 0, ErrSchema
}
