#!/usr/bin/env python3
from pathlib import Path


def replace_once(path, old, new):
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected one match, found {count}: {old[:80]!r}")
    p.write_text(text.replace(old, new, 1))


replace_once(
    "internal/api/server_test.go",
    '\tmanagedXray "github.com/egress-manager/egress-manager/internal/xray"\n',
    '\tmanagedXray "github.com/egress-manager/egress-manager/internal/xray"\n\t"github.com/egress-manager/egress-manager/internal/xrayrelay"\n',
)
replace_once(
    "internal/api/server_test.go",
    'func (control *healthyControl) PlanRoutes(context.Context) (routeengine.Review, error) {',
    '''func (control *healthyControl) ImportXrayRelayOutbound(_ context.Context, request xrayrelay.ImportRequest) (xrayrelay.ImportResponse, error) {
\tcontrol.calls++
\treturn xrayrelay.ImportResponse{}, nil
}

func (control *healthyControl) TestXrayRelayOutbound(_ context.Context, request xrayrelay.TestRequest) (xrayrelay.TestResponse, error) {
\tcontrol.calls++
\treturn xrayrelay.TestResponse{}, nil
}

func (control *healthyControl) PlanRelays(context.Context) (xrayrelay.Plan, error) {
\tcontrol.calls++
\treturn xrayrelay.Plan{Engine: "xray-relay", StateHash: "state", CandidateHash: "candidate"}, nil
}

func (control *healthyControl) ApplyRelays(_ context.Context, request xrayrelay.ApplyRequest) (xrayrelay.ApplyResponse, error) {
\tcontrol.calls++
\treturn xrayrelay.ApplyResponse{TransactionID: request.TransactionID, State: domain.TransactionCommitted, CandidateHash: request.ExpectedCandidateHash}, nil
}

func (control *healthyControl) PlanRoutes(context.Context) (routeengine.Review, error) {''',
)
replace_once(
    "internal/api/server_test.go",
    '\t\tstore,\n\t\thealth,\n',
    '\t\tstore,\n\t\tstore,\n\t\thealth,\n',
)
replace_once(
    "internal/database/database_test.go",
    'if count != 7 {\n\t\tt.Fatalf("migration count = %d, want 7", count)\n',
    'if count != 8 {\n\t\tt.Fatalf("migration count = %d, want 8", count)\n',
)
replace_once(
    "internal/app/app_test.go",
    'if !recoveryReport.Succeeded || len(recoveryReport.Steps) != 10 {',
    'if !recoveryReport.Succeeded || len(recoveryReport.Steps) != 11 {',
)
