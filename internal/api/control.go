package api

import (
	"context"

	"github.com/egress-manager/egress-manager/internal/ipc"
)

type IPCControl struct {
	Client ipc.Client
}

func (control IPCControl) Health(ctx context.Context) error {
	var response struct {
		Status string `json:"status"`
	}
	return control.Client.Call(ctx, ipc.OperationHealth, struct{}{}, &response)
}
