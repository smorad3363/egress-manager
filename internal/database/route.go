package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/egress-manager/egress-manager/internal/domain"
)

type StoredRoute struct {
	Route     domain.Route `json:"route"`
	Revision  int64        `json:"revision"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func (store *Store) CreateRoute(ctx context.Context, route domain.Route, at time.Time) (StoredRoute, error) {
	definition, sourceValue, err := encodeRoute(route)
	if err != nil {
		return StoredRoute{}, err
	}
	if at.IsZero() {
		return StoredRoute{}, fmt.Errorf("route creation time is required")
	}
	at = at.UTC().Truncate(time.Second)
	fallback := nullableID(route.FallbackOutboundID)
	_, err = store.database.ExecContext(ctx, `
        INSERT INTO egress_routes(id, name, source_kind, source_value, outbound_id, fallback_outbound_id, definition, enabled, revision, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
    `, string(route.ID), route.Name, string(route.Source.Kind), sourceValue, string(route.OutboundID), fallback, string(definition), route.Enabled, at.Unix(), at.Unix())
	if err := mapWriteError("create route", err); err != nil {
		return StoredRoute{}, err
	}
	return StoredRoute{Route: route, Revision: 1, CreatedAt: at, UpdatedAt: at}, nil
}

func (store *Store) UpdateRoute(ctx context.Context, route domain.Route, expectedRevision int64, at time.Time) (StoredRoute, error) {
	definition, sourceValue, err := encodeRoute(route)
	if err != nil {
		return StoredRoute{}, err
	}
	if expectedRevision < 1 || at.IsZero() {
		return StoredRoute{}, fmt.Errorf("expected revision and update time are required")
	}
	at = at.UTC().Truncate(time.Second)
	result, err := store.database.ExecContext(ctx, `
        UPDATE egress_routes
        SET name = ?, source_kind = ?, source_value = ?, outbound_id = ?, fallback_outbound_id = ?, definition = ?, enabled = ?, revision = revision + 1, updated_at = ?
        WHERE id = ? AND revision = ?
    `, route.Name, string(route.Source.Kind), sourceValue, string(route.OutboundID), nullableID(route.FallbackOutboundID), string(definition), route.Enabled, at.Unix(), string(route.ID), expectedRevision)
	if err := mapWriteError("update route", err); err != nil {
		return StoredRoute{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return StoredRoute{}, fmt.Errorf("update route: read affected rows: %w", err)
	}
	if count != 1 {
		return StoredRoute{}, ErrConflict
	}
	return store.Route(ctx, route.ID)
}

func (store *Store) DeleteRoute(ctx context.Context, id domain.ID, expectedRevision int64) error {
	if err := id.Validate("route id"); err != nil {
		return err
	}
	if expectedRevision < 1 {
		return fmt.Errorf("expected revision is required")
	}
	result, err := store.database.ExecContext(ctx, `DELETE FROM egress_routes WHERE id = ? AND revision = ?`, string(id), expectedRevision)
	if err := mapWriteError("delete route", err); err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete route: read affected rows: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (store *Store) Route(ctx context.Context, id domain.ID) (StoredRoute, error) {
	if err := id.Validate("route id"); err != nil {
		return StoredRoute{}, err
	}
	return scanRoute(store.database.QueryRowContext(ctx, `SELECT definition, revision, created_at, updated_at FROM egress_routes WHERE id = ?`, string(id)))
}

func (store *Store) ListRoutes(ctx context.Context, after domain.ID, limit int) ([]StoredRoute, error) {
	if after != "" {
		if err := after.Validate("route cursor"); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > 501 {
		return nil, fmt.Errorf("route list limit must be between 1 and 501")
	}
	rows, err := store.database.QueryContext(ctx, `SELECT definition, revision, created_at, updated_at FROM egress_routes WHERE id > ? ORDER BY id LIMIT ?`, string(after), limit)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	defer rows.Close()
	items := make([]StoredRoute, 0)
	for rows.Next() {
		item, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate routes: %w", err)
	}
	return items, nil
}

func encodeRoute(route domain.Route) ([]byte, string, error) {
	if err := route.Validate(); err != nil {
		return nil, "", fmt.Errorf("validate route: %w", err)
	}
	definition, err := json.Marshal(route)
	if err != nil {
		return nil, "", fmt.Errorf("encode route: %w", err)
	}
	if len(definition) > 65536 {
		return nil, "", fmt.Errorf("route definition exceeds 65536 bytes")
	}
	sourceValue := route.Source.Interface
	if route.Source.Kind == domain.RouteSourceSubnet {
		sourceValue = route.Source.Subnet.String()
	} else if route.Source.Kind == domain.RouteSourceListener {
		sourceValue = fmt.Sprint(route.Source.Listener)
	} else if route.Source.Kind == domain.RouteSourceXrayInbound {
		sourceValue = route.Source.XrayTag
	}
	return definition, sourceValue, nil
}

func scanRoute(scanner operationScanner) (StoredRoute, error) {
	var stored StoredRoute
	var definition string
	var createdAt, updatedAt int64
	if err := scanner.Scan(&definition, &stored.Revision, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredRoute{}, ErrNotFound
		}
		return StoredRoute{}, fmt.Errorf("scan route: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(definition))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored.Route); err != nil {
		return StoredRoute{}, fmt.Errorf("decode stored route: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return StoredRoute{}, fmt.Errorf("decode stored route: trailing data")
	}
	if err := stored.Route.Validate(); err != nil {
		return StoredRoute{}, fmt.Errorf("validate stored route: %w", err)
	}
	stored.CreatedAt = time.Unix(createdAt, 0).UTC()
	stored.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return stored, nil
}

func nullableID(id domain.ID) any {
	if id == "" {
		return nil
	}
	return string(id)
}
