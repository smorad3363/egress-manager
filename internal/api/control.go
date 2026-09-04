package api

import (
	"context"
	"fmt"

	managedHAProxy "github.com/egress-manager/egress-manager/internal/haproxy"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/nat"
	"github.com/egress-manager/egress-manager/internal/routeengine"
	managedSingBox "github.com/egress-manager/egress-manager/internal/singbox"
	managedXray "github.com/egress-manager/egress-manager/internal/xray"
)

func (control IPCControl) DiscoverXray(ctx context.Context) (managedXray.Report, error) {
	var response managedXray.Report
	if err := control.Client.Call(ctx, ipc.OperationXrayDiscover, struct{}{}, &response); err != nil {
		return managedXray.Report{}, err
	}
	return response, nil
}

func (control IPCControl) PlanXray(ctx context.Context) (managedXray.FragmentReview, error) {
	var response managedXray.FragmentReview
	if err := control.Client.Call(ctx, ipc.OperationXrayPlan, struct{}{}, &response); err != nil {
		return managedXray.FragmentReview{}, err
	}
	return response, nil
}

func (control IPCControl) ApplyXray(ctx context.Context, request managedXray.FragmentApplyRequest) (managedXray.FragmentApplyResponse, error) {
	var response managedXray.FragmentApplyResponse
	if err := control.Client.Call(ctx, ipc.OperationXrayApply, request, &response); err != nil {
		return managedXray.FragmentApplyResponse{}, err
	}
	return response, nil
}

func (control IPCControl) PlanHAProxy(ctx context.Context, request managedHAProxy.PlanRequest) (managedHAProxy.Plan, error) {
	var response managedHAProxy.Plan
	if err := control.Client.Call(ctx, ipc.OperationHAProxyPlan, request, &response); err != nil {
		return managedHAProxy.Plan{}, err
	}
	return response, nil
}

func (control IPCControl) PlanRoutes(ctx context.Context) (routeengine.Review, error) {
	var response routeengine.Review
	if err := control.Client.Call(ctx, ipc.OperationRoutesPlan, struct{}{}, &response); err != nil {
		return routeengine.Review{}, err
	}
	return response, nil
}

func (control IPCControl) ApplyRoutes(ctx context.Context, request routeengine.ApplyRequest) (routeengine.ApplyResponse, error) {
	var response routeengine.ApplyResponse
	if err := control.Client.Call(ctx, ipc.OperationRoutesApply, request, &response); err != nil {
		return routeengine.ApplyResponse{}, err
	}
	return response, nil
}

func (control IPCControl) ImportSingBox(ctx context.Context, request managedSingBox.ImportRequest) (managedSingBox.ImportResponse, error) {
	var response managedSingBox.ImportResponse
	if err := control.Client.Call(ctx, ipc.OperationSingBoxImport, request, &response); err != nil {
		return managedSingBox.ImportResponse{}, err
	}
	return response, nil
}

func (control IPCControl) TestSingBox(ctx context.Context, request managedSingBox.TestRequest) (managedSingBox.TestResponse, error) {
	var response managedSingBox.TestResponse
	if err := control.Client.Call(ctx, ipc.OperationSingBoxTest, request, &response); err != nil {
		return managedSingBox.TestResponse{}, err
	}
	return response, nil
}

func (control IPCControl) PlanSingBox(ctx context.Context) (managedSingBox.Plan, error) {
	var response managedSingBox.Plan
	if err := control.Client.Call(ctx, ipc.OperationSingBoxPlan, struct{}{}, &response); err != nil {
		return managedSingBox.Plan{}, err
	}
	return response, nil
}

func (control IPCControl) ApplySingBox(ctx context.Context, request managedSingBox.ApplyRequest) (managedSingBox.ApplyResponse, error) {
	var response managedSingBox.ApplyResponse
	if err := control.Client.Call(ctx, ipc.OperationSingBoxApply, request, &response); err != nil {
		return managedSingBox.ApplyResponse{}, err
	}
	return response, nil
}

func (control IPCControl) ApplyHAProxy(ctx context.Context, request managedHAProxy.ApplyRequest) (managedHAProxy.ApplyResponse, error) {
	var response managedHAProxy.ApplyResponse
	if err := control.Client.Call(ctx, ipc.OperationHAProxyApply, request, &response); err != nil {
		return managedHAProxy.ApplyResponse{}, err
	}
	return response, nil
}

func (control IPCControl) HAProxyStats(ctx context.Context) (managedHAProxy.RuntimeSnapshot, error) {
	var response managedHAProxy.RuntimeSnapshot
	if err := control.Client.Call(ctx, ipc.OperationHAProxyStats, struct{}{}, &response); err != nil {
		return managedHAProxy.RuntimeSnapshot{}, err
	}
	return response, nil
}

type IPCControl struct {
	Client ipc.Client
}

func (control IPCControl) Inventory(ctx context.Context) (inventory.Inventory, error) {
	var response inventory.Inventory
	if err := control.Client.Call(ctx, ipc.OperationInventory, struct{}{}, &response); err != nil {
		return inventory.Inventory{}, err
	}
	return response, nil
}

func (control IPCControl) Health(ctx context.Context) error {
	var response struct {
		Status string `json:"status"`
	}
	if err := control.Client.Call(ctx, ipc.OperationHealth, struct{}{}, &response); err != nil {
		return err
	}
	if response.Status != "ok" {
		return fmt.Errorf("egressd returned an unhealthy status")
	}
	return nil
}

func (control IPCControl) PlanNAT(ctx context.Context, request nat.PlanRequest) (nat.Plan, error) {
	var response nat.Plan
	if err := control.Client.Call(ctx, ipc.OperationNATPlan, request, &response); err != nil {
		return nat.Plan{}, err
	}
	return response, nil
}

func (control IPCControl) ApplyNAT(ctx context.Context, request nat.ApplyRequest) (nat.ApplyResponse, error) {
	var response nat.ApplyResponse
	if err := control.Client.Call(ctx, ipc.OperationNATApply, request, &response); err != nil {
		return nat.ApplyResponse{}, err
	}
	return response, nil
}

func (control IPCControl) NATCounters(ctx context.Context, request nat.CounterRequest) (nat.CounterSnapshot, error) {
	var response nat.CounterSnapshot
	if err := control.Client.Call(ctx, ipc.OperationNATCount, request, &response); err != nil {
		return nat.CounterSnapshot{}, err
	}
	return response, nil
}
