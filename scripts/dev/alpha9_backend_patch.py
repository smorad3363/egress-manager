#!/usr/bin/env python3
from pathlib import Path


def replace_once(path, old, new):
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected one match, found {count}: {old[:80]!r}")
    p.write_text(text.replace(old, new, 1))


def insert_before(path, marker, text):
    replace_once(path, marker, text + marker)


# Configuration: add a backward-compatible path for the project-owned relay runtime.
replace_once(
    "internal/config/config.go",
    '\tSingBoxConfigPath         string         `json:"sing_box_config_path"`\n\tRoutingStatePath          string         `json:"routing_state_path"`',
    '\tSingBoxConfigPath         string         `json:"sing_box_config_path"`\n\tXrayRelayConfigPath       string         `json:"xray_relay_config_path"`\n\tRoutingStatePath          string         `json:"routing_state_path"`',
)
replace_once(
    "internal/config/config.go",
    '\t\tSingBoxConfigPath:         filepath.Join(dataDirectory, "sing-box.json"),\n\t\tRoutingStatePath:',
    '\t\tSingBoxConfigPath:         filepath.Join(dataDirectory, "sing-box.json"),\n\t\tXrayRelayConfigPath:       filepath.Join(dataDirectory, "xray-relay.json"),\n\t\tRoutingStatePath:',
)
replace_once(
    "internal/config/config.go",
    '\t\t"sing_box_config_path":        configuration.SingBoxConfigPath,\n\t\t"routing_state_path":',
    '\t\t"sing_box_config_path":        configuration.SingBoxConfigPath,\n\t\t"xray_relay_config_path":      configuration.XrayRelayConfigPath,\n\t\t"routing_state_path":',
)
replace_once(
    "internal/config/config.go",
    'configuration.SingBoxConfigPath, configuration.RoutingStatePath,',
    'configuration.SingBoxConfigPath, configuration.XrayRelayConfigPath, configuration.RoutingStatePath,',
)
insert_before(
    "internal/config/config.go",
    '\tif configuration.RoutingStatePath == "" && filepath.IsAbs(configuration.DataDirectory) {',
    '\tif configuration.XrayRelayConfigPath == "" && filepath.IsAbs(configuration.DataDirectory) {\n\t\tconfiguration.XrayRelayConfigPath = filepath.Join(configuration.DataDirectory, "xray-relay.json")\n\t}\n',
)
replace_once(
    "packaging/config.json.in",
    '  "sing_box_config_path": "/var/lib/egress-manager/private/sing-box.json",\n',
    '  "sing_box_config_path": "/var/lib/egress-manager/private/sing-box.json",\n  "xray_relay_config_path": "/var/lib/egress-manager/private/xray-relay.json",\n',
)

# IPC operations.
replace_once(
    "internal/ipc/protocol.go",
    '\tOperationSingBoxApply     Operation = "singbox.apply"\n\tOperationRoutesPlan',
    '\tOperationSingBoxApply     Operation = "singbox.apply"\n\tOperationXrayRelayImport   Operation = "xrayrelay.import"\n\tOperationXrayRelayTest     Operation = "xrayrelay.test"\n\tOperationRelayPlan         Operation = "relay.plan"\n\tOperationRelayApply        Operation = "relay.apply"\n\tOperationRoutesPlan',
)
replace_once(
    "internal/ipc/protocol.go",
    'OperationSingBoxPlan, OperationSingBoxApply, OperationRoutesPlan',
    'OperationSingBoxPlan, OperationSingBoxApply, OperationXrayRelayImport, OperationXrayRelayTest, OperationRelayPlan, OperationRelayApply, OperationRoutesPlan',
)

# API control client.
replace_once(
    "internal/api/control.go",
    '\tmanagedXray "github.com/egress-manager/egress-manager/internal/xray"\n',
    '\tmanagedXray "github.com/egress-manager/egress-manager/internal/xray"\n\t"github.com/egress-manager/egress-manager/internal/xrayrelay"\n',
)
insert_before(
    "internal/api/control.go",
    'func (control IPCControl) ImportInterfaceOutbound',
    '''func (control IPCControl) ImportXrayRelayOutbound(ctx context.Context, request xrayrelay.ImportRequest) (xrayrelay.ImportResponse, error) {
\tvar response xrayrelay.ImportResponse
\tif err := control.Client.Call(ctx, ipc.OperationXrayRelayImport, request, &response); err != nil {
\t\treturn xrayrelay.ImportResponse{}, err
\t}
\treturn response, nil
}

func (control IPCControl) TestXrayRelayOutbound(ctx context.Context, request xrayrelay.TestRequest) (xrayrelay.TestResponse, error) {
\tvar response xrayrelay.TestResponse
\tif err := control.Client.Call(ctx, ipc.OperationXrayRelayTest, request, &response); err != nil {
\t\treturn xrayrelay.TestResponse{}, err
\t}
\treturn response, nil
}

func (control IPCControl) PlanRelays(ctx context.Context) (xrayrelay.Plan, error) {
\tvar response xrayrelay.Plan
\tif err := control.Client.Call(ctx, ipc.OperationRelayPlan, struct{}{}, &response); err != nil {
\t\treturn xrayrelay.Plan{}, err
\t}
\treturn response, nil
}

func (control IPCControl) ApplyRelays(ctx context.Context, request xrayrelay.ApplyRequest) (xrayrelay.ApplyResponse, error) {
\tvar response xrayrelay.ApplyResponse
\tif err := control.Client.Call(ctx, ipc.OperationRelayApply, request, &response); err != nil {
\t\treturn xrayrelay.ApplyResponse{}, err
\t}
\treturn response, nil
}

''',
)

# API server interfaces, dependencies, and routes.
replace_once(
    "internal/api/server.go",
    '\tmanagedXray "github.com/egress-manager/egress-manager/internal/xray"\n',
    '\tmanagedXray "github.com/egress-manager/egress-manager/internal/xray"\n\t"github.com/egress-manager/egress-manager/internal/xrayrelay"\n',
)
replace_once(
    "internal/api/server.go",
    '\tApplySingBox(context.Context, managedSingBox.ApplyRequest) (managedSingBox.ApplyResponse, error)\n\tPlanRoutes',
    '\tApplySingBox(context.Context, managedSingBox.ApplyRequest) (managedSingBox.ApplyResponse, error)\n\tImportXrayRelayOutbound(context.Context, xrayrelay.ImportRequest) (xrayrelay.ImportResponse, error)\n\tTestXrayRelayOutbound(context.Context, xrayrelay.TestRequest) (xrayrelay.TestResponse, error)\n\tPlanRelays(context.Context) (xrayrelay.Plan, error)\n\tApplyRelays(context.Context, xrayrelay.ApplyRequest) (xrayrelay.ApplyResponse, error)\n\tPlanRoutes',
)
insert_before(
    "internal/api/server.go",
    'type XrayBindingRepository interface {',
    '''type RelayRepository interface {
\tCreateRelay(context.Context, domain.Relay, time.Time) (database.StoredRelay, error)
\tUpdateRelay(context.Context, domain.Relay, int64, time.Time) (database.StoredRelay, error)
\tDeleteRelay(context.Context, domain.ID, int64) error
\tListRelays(context.Context, domain.ID, int) ([]database.StoredRelay, error)
}

''',
)
replace_once(
    "internal/api/server.go",
    '\troutes    RouteRepository\n\txray      XrayBindingRepository',
    '\troutes    RouteRepository\n\trelays    RelayRepository\n\txray      XrayBindingRepository',
)
replace_once(
    "internal/api/server.go",
    'func NewServer(config ServerConfig, logger *slog.Logger, login LoginService, sessions SessionService, control ControlService, forwards ForwardRepository, haproxy HAProxyRepository, outbounds OutboundRepository, routes RouteRepository, xray XrayBindingRepository, health http.Handler)',
    'func NewServer(config ServerConfig, logger *slog.Logger, login LoginService, sessions SessionService, control ControlService, forwards ForwardRepository, haproxy HAProxyRepository, outbounds OutboundRepository, routes RouteRepository, relays RelayRepository, xray XrayBindingRepository, health http.Handler)',
)
replace_once(
    "internal/api/server.go",
    'outbounds == nil || routes == nil || xray == nil || health == nil',
    'outbounds == nil || routes == nil || relays == nil || xray == nil || health == nil',
)
replace_once(
    "internal/api/server.go",
    '\t\troutes:    routes,\n\t\txray:      xray,',
    '\t\troutes:    routes,\n\t\trelays:    relays,\n\t\txray:      xray,',
)
replace_once(
    "internal/api/server.go",
    '\tmux.Handle("/api/v1/routes", server.requireSession(http.HandlerFunc(server.routesHandler)))\n',
    '\tmux.Handle("/api/v1/routes", server.requireSession(http.HandlerFunc(server.routesHandler)))\n\tmux.Handle("/api/v1/relays/plan", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.planRelaysHandler))))\n\tmux.Handle("/api/v1/relays/apply", server.method(http.MethodPost, server.requireSession(http.HandlerFunc(server.applyRelaysHandler))))\n\tmux.Handle("/api/v1/relays", server.requireSession(http.HandlerFunc(server.relaysHandler)))\n',
)

# Dispatch imported/tested XHTTP+REALITY VLESS links to Xray instead of sing-box.
replace_once(
    "internal/api/outbounds.go",
    '\tmanagedSingBox "github.com/egress-manager/egress-manager/internal/singbox"\n',
    '\tmanagedSingBox "github.com/egress-manager/egress-manager/internal/singbox"\n\t"github.com/egress-manager/egress-manager/internal/xrayrelay"\n',
)
replace_once(
    "internal/api/outbounds.go",
    '''\tresult, err := server.control.ImportSingBox(request.Context(), input)
\tif err != nil {
\t\tWriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Outbound import rejected.", err))
\t\treturn
\t}
\t_ = WriteJSON(writer, http.StatusCreated, result)
''',
    '''\tif xrayrelay.IsCompatibleImport(input.Input) {
\t\tresult, err := server.control.ImportXrayRelayOutbound(request.Context(), xrayrelay.ImportRequest{Input: input.Input})
\t\tif err != nil {
\t\t\tWriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Outbound import rejected.", err))
\t\t\treturn
\t\t}
\t\t_ = WriteJSON(writer, http.StatusCreated, result)
\t\treturn
\t}
\tresult, err := server.control.ImportSingBox(request.Context(), input)
\tif err != nil {
\t\tWriteError(writer, request, NewError(http.StatusConflict, CodeConflict, "Outbound import rejected.", err))
\t\treturn
\t}
\t_ = WriteJSON(writer, http.StatusCreated, result)
''',
)
old_test = '''\tresult, err := server.control.TestSingBox(request.Context(), input)
\tif err != nil {
\t\tWriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Outbound test failed.", err))
\t\treturn
\t}
\t_ = WriteJSON(writer, http.StatusOK, result)
'''
new_test = '''\tuseXray := input.Input != "" && xrayrelay.IsCompatibleImport(input.Input)
\tif input.ID != "" {
\t\tstored, lookupErr := server.outbounds.Outbound(request.Context(), input.ID)
\t\tif lookupErr != nil {
\t\t\tWriteError(writer, request, NewError(http.StatusNotFound, CodeNotFound, "Outbound not found.", lookupErr))
\t\t\treturn
\t\t}
\t\tuseXray = stored.Outbound.Adapter == domain.OutboundAdapterXray
\t}
\tif useXray {
\t\tresult, err := server.control.TestXrayRelayOutbound(request.Context(), xrayrelay.TestRequest{ID: input.ID, ExpectedRevision: input.ExpectedRevision, Input: input.Input})
\t\tif err != nil {
\t\t\tWriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Outbound test failed.", err))
\t\t\treturn
\t\t}
\t\t_ = WriteJSON(writer, http.StatusOK, result)
\t\treturn
\t}
\tresult, err := server.control.TestSingBox(request.Context(), input)
\tif err != nil {
\t\tWriteError(writer, request, NewError(http.StatusServiceUnavailable, CodeUnavailable, "Outbound test failed.", err))
\t\treturn
\t}
\t_ = WriteJSON(writer, http.StatusOK, result)
'''
replace_once("internal/api/outbounds.go", old_test, new_test)

# Web service passes the same protected store as relay repository.
replace_once(
    "internal/app/web.go",
    '}, logger, authentication, sessions, control, store, store, store, store, store, health)',
    '}, logger, authentication, sessions, control, store, store, store, store, store, store, health)',
)

# Daemon imports and executor.
replace_once(
    "internal/app/daemon.go",
    '\tmanagedXray "github.com/egress-manager/egress-manager/internal/xray"\n',
    '\tmanagedXray "github.com/egress-manager/egress-manager/internal/xray"\n\t"github.com/egress-manager/egress-manager/internal/xrayrelay"\n',
)
replace_once(
    "internal/app/daemon.go",
    '\tsingboxExecutor := managedSingBox.Executor{Runner: runner, Journal: store, Protector: protector, ConfigPath: configuration.SingBoxConfigPath}\n',
    '\tsingboxExecutor := managedSingBox.Executor{Runner: runner, Journal: store, Protector: protector, ConfigPath: configuration.SingBoxConfigPath}\n\txrayRelayExecutor := xrayrelay.Executor{Runner: runner, Journal: store, Protector: protector, ConfigPath: configuration.XrayRelayConfigPath}\n',
)
replace_once(
    "internal/app/daemon.go",
    '\t\t\t{Component: "singbox", Recover: singboxExecutor.Recover},\n',
    '\t\t\t{Component: "singbox", Recover: singboxExecutor.Recover},\n\t\t\t{Component: "xray_relay", Recover: xrayRelayExecutor.Recover},\n',
)
replace_once(
    "internal/app/daemon.go",
    '''\t\t\tcase "singbox_apply":
\t\t\t\tif err := singboxExecutor.RollbackCommitted(ctx, operation); err != nil {
\t\t\t\t\treturn nil, err
\t\t\t\t}
\t\t\t\tcomponent = "singbox"
''',
    '''\t\t\tcase "singbox_apply":
\t\t\t\tif err := singboxExecutor.RollbackCommitted(ctx, operation); err != nil {
\t\t\t\t\treturn nil, err
\t\t\t\t}
\t\t\t\tcomponent = "singbox"
\t\t\tcase "xray_relay_apply":
\t\t\t\tif err := xrayRelayExecutor.RollbackCommitted(ctx, operation); err != nil {
\t\t\t\t\treturn nil, err
\t\t\t\t}
\t\t\t\tcomponent = "xray_relay"
''',
)

relay_handlers = r'''\tif err := server.Handle(ipc.OperationXrayRelayImport, func(ctx context.Context, payload json.RawMessage) (any, error) {
\t\tvar request xrayrelay.ImportRequest
\t\tif err := decodePayload(payload, &request); err != nil {
\t\t\treturn nil, err
\t\t}
\t\toutboundMutex.Lock()
\t\tdefer outboundMutex.Unlock()
\t\tparsed, err := xrayrelay.ParseImport(request.Input)
\t\tif err != nil {
\t\t\treturn nil, err
\t\t}
\t\tstored, err := store.CreateOutbound(ctx, parsed.Outbound, parsed.CredentialDocument, time.Now().UTC())
\t\tif err != nil {
\t\t\treturn nil, err
\t\t}
\t\treturn xrayrelay.ImportResponse{Outbounds: []domain.Outbound{stored.Outbound}}, nil
\t}); err != nil {
\t\treturn err
\t}
\tif err := server.Handle(ipc.OperationXrayRelayTest, func(ctx context.Context, payload json.RawMessage) (any, error) {
\t\tvar request xrayrelay.TestRequest
\t\tif err := decodePayload(payload, &request); err != nil {
\t\t\treturn nil, err
\t\t}
\t\toutboundMutex.Lock()
\t\tdefer outboundMutex.Unlock()
\t\tif (request.ID == "") == (request.Input == "") {
\t\t\treturn nil, fmt.Errorf("exactly one stored Xray outbound ID or import input is required")
\t\t}
\t\ttester := xrayrelay.Tester{Runner: runner, Dialer: &net.Dialer{Timeout: 2 * time.Second}, Timeout: 10 * time.Second}
\t\tif request.ID != "" {
\t\t\tif request.ExpectedRevision < 1 {
\t\t\t\treturn nil, fmt.Errorf("expected revision is required")
\t\t\t}
\t\t\tstored, err := store.Outbound(ctx, request.ID)
\t\t\tif err != nil {
\t\t\t\treturn nil, err
\t\t\t}
\t\t\tif stored.Revision != request.ExpectedRevision {
\t\t\t\treturn nil, database.ErrConflict
\t\t\t}
\t\t\tif stored.Outbound.Adapter != domain.OutboundAdapterXray {
\t\t\t\treturn nil, fmt.Errorf("outbound does not use the Xray adapter")
\t\t\t}
\t\t\tcredential, err := store.OutboundCredential(ctx, request.ID)
\t\t\tif err != nil {
\t\t\t\treturn nil, err
\t\t\t}
\t\t\thealth := tester.Test(ctx, stored.Outbound, credential)
\t\t\tstored.Outbound.Health = health
\t\t\tupdated, err := store.UpdateOutbound(ctx, stored.Outbound, request.ExpectedRevision, nil, time.Now().UTC())
\t\t\tif err != nil {
\t\t\t\treturn nil, err
\t\t\t}
\t\t\treturn xrayrelay.TestResponse{Results: []xrayrelay.TestResult{{Outbound: updated.Outbound, Health: health}}}, nil
\t\t}
\t\tparsed, err := xrayrelay.ParseImport(request.Input)
\t\tif err != nil {
\t\t\treturn nil, err
\t\t}
\t\thealth := tester.Test(ctx, parsed.Outbound, parsed.CredentialDocument)
\t\treturn xrayrelay.TestResponse{Results: []xrayrelay.TestResult{{Outbound: parsed.Outbound, Health: health}}}, nil
\t}); err != nil {
\t\treturn err
\t}
\tloadRelayPlan := func(ctx context.Context) (xrayrelay.ExecutionPlan, error) {
\t\tstoredRelays, err := store.ListRelays(ctx, "", xrayrelay.MaximumRelays+1)
\t\tif err != nil {
\t\t\treturn xrayrelay.ExecutionPlan{}, err
\t\t}
\t\tif len(storedRelays) > xrayrelay.MaximumRelays {
\t\t\treturn xrayrelay.ExecutionPlan{}, fmt.Errorf("relay desired state exceeds %d entries", xrayrelay.MaximumRelays)
\t\t}
\t\trelays := make([]domain.Relay, 0, len(storedRelays))
\t\treferenced := make(map[domain.ID]struct{})
\t\tfor _, item := range storedRelays {
\t\t\trelays = append(relays, item.Relay)
\t\t\tif item.Relay.Enabled {
\t\t\t\treferenced[item.Relay.OutboundID] = struct{}{}
\t\t\t}
\t\t}
\t\tstoredOutbounds, err := store.ListOutbounds(ctx, "", managedSingBox.MaximumOutbounds+1)
\t\tif err != nil {
\t\t\treturn xrayrelay.ExecutionPlan{}, err
\t\t}
\t\toutbounds := make([]domain.Outbound, 0, len(storedOutbounds))
\t\tcredentials := make(map[domain.ID][]byte)
\t\tfor _, item := range storedOutbounds {
\t\t\toutbounds = append(outbounds, item.Outbound)
\t\t\tif _, needed := referenced[item.Outbound.ID]; needed {
\t\t\t\tdocument, credentialErr := store.OutboundCredential(ctx, item.Outbound.ID)
\t\t\t\tif credentialErr != nil {
\t\t\t\t\treturn xrayrelay.ExecutionPlan{}, credentialErr
\t\t\t\t}
\t\t\t\tcredentials[item.Outbound.ID] = document
\t\t\t}
\t\t}
\t\thost, err := collector.Collect(ctx)
\t\tif err != nil {
\t\t\treturn xrayrelay.ExecutionPlan{}, err
\t\t}
\t\tprotectedPorts := append([]uint16{configuration.ListenPort}, configuration.SSHPorts...)
\t\tfor _, listener := range host.Listeners {
\t\t\tif strings.EqualFold(listener.Process, "sshd") && listener.Protocol == "tcp" && listener.Port != 0 && !containsPort(protectedPorts, listener.Port) {
\t\t\t\tprotectedPorts = append(protectedPorts, listener.Port)
\t\t\t}
\t\t}
\t\tstate, err := xrayrelay.InspectState(configuration.XrayRelayConfigPath)
\t\tif err != nil {
\t\t\treturn xrayrelay.ExecutionPlan{}, err
\t\t}
\t\treturn xrayrelay.BuildPlan(xrayrelay.Settings{ProtectedPorts: protectedPorts}, relays, outbounds, credentials, host.Listeners, state)
\t}
\tif err := server.Handle(ipc.OperationRelayPlan, func(ctx context.Context, payload json.RawMessage) (any, error) {
\t\tif err := decodeEmptyPayload(payload); err != nil {
\t\t\treturn nil, err
\t\t}
\t\toutboundMutex.Lock()
\t\tdefer outboundMutex.Unlock()
\t\tplan, err := loadRelayPlan(ctx)
\t\tif err != nil {
\t\t\treturn nil, err
\t\t}
\t\treturn plan.Review, nil
\t}); err != nil {
\t\treturn err
\t}
\tif err := server.Handle(ipc.OperationRelayApply, func(ctx context.Context, payload json.RawMessage) (any, error) {
\t\tvar request xrayrelay.ApplyRequest
\t\tif err := decodePayload(payload, &request); err != nil {
\t\t\treturn nil, err
\t\t}
\t\toutboundMutex.Lock()
\t\tdefer outboundMutex.Unlock()
\t\treturn mutate(ctx, request.TransactionID, "xray_relay", func() (any, error) {
\t\t\tplan, err := loadRelayPlan(ctx)
\t\t\tif err != nil {
\t\t\t\treturn nil, err
\t\t\t}
\t\t\tif request.ExpectedStateHash == "" || request.ExpectedCandidateHash == "" || plan.Review.StateHash != request.ExpectedStateHash || plan.Review.CandidateHash != request.ExpectedCandidateHash {
\t\t\t\treturn nil, xrayrelay.ErrStateChanged
\t\t\t}
\t\t\treturn xrayRelayExecutor.Execute(ctx, request.TransactionID, plan)
\t\t})
\t}); err != nil {
\t\treturn err
\t}

'''.replace('\\t', '\t')
insert_before("internal/app/daemon.go", '\tif err := server.Handle(ipc.OperationInterfaceImport', relay_handlers)

# Installer installs/enables the conditional relay unit. It is inert until a relay config exists.
replace_once(
    "scripts/install/install.sh",
    'install -o root -g root -m 0644 "${package_directory}/systemd/egress-manager-sing-box.service" /etc/systemd/system/egress-manager-sing-box.service\n',
    'install -o root -g root -m 0644 "${package_directory}/systemd/egress-manager-sing-box.service" /etc/systemd/system/egress-manager-sing-box.service\ninstall -o root -g root -m 0644 "${package_directory}/systemd/egress-manager-xray-relay.service" /etc/systemd/system/egress-manager-xray-relay.service\n',
)
replace_once(
    "scripts/install/install.sh",
    'systemctl enable egressd.service egress-web.service\n',
    'systemctl enable egressd.service egress-web.service egress-manager-xray-relay.service\n',
)

# Fix/import adapter constant and strict trailing JSON handling.
replace_once(
    "internal/xrayrelay/import.go",
    'Adapter:      domain.OutboundAdapter("xray"),',
    'Adapter:      domain.OutboundAdapterXray,',
)
replace_once(
    "internal/xrayrelay/import.go",
    '"fmt"\n\t"net/url"',
    '"fmt"\n\t"io"\n\t"net/url"',
)
replace_once(
    "internal/xrayrelay/import.go",
    '''\t\tvar trailing any
\t\tif err := decoder.Decode(&trailing); err == nil {
\t\t\treturn ImportResult{}, fmt.Errorf("XHTTP extra JSON has trailing data")
\t\t}
''',
    '''\t\tvar trailing any
\t\tif err := decoder.Decode(&trailing); err != io.EOF {
\t\t\treturn ImportResult{}, fmt.Errorf("XHTTP extra JSON has trailing data")
\t\t}
''',
)
