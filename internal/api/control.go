package api

import (
	"context"
	"fmt"

	"github.com/egress-manager/egress-manager/internal/inventory"
	"github.com/egress-manager/egress-manager/internal/ipc"
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
