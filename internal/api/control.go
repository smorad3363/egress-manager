package api

import (
	"context"
	"fmt"

	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/ipc"
	"github.com/egress-manager/egress-manager/internal/nat"
)

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
