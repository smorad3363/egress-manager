package api

import (
	"context"
	"fmt"

	managedHAProxy "github.com/egress-manager/egress-manager/internal/haproxy"
	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/nat"
)

func (control IPCControl) PlanHAProxy(ctx context.Context, request managedHAProxy.PlanRequest) (managedHAProxy.Plan, error) {
	var response managedHAProxy.Plan
	if err := control.Client.Call(ctx, ipc.OperationHAProxyPlan, request, &response); err != nil {
		return managedHAProxy.Plan{}, err
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
