// Package basic demonstrates compschema with Order, LineItem, and Shape types.
package basic

//go:generate compschema generate ./...

// OrderStatus is an enum of valid order states.
type OrderStatus string

const (
	OrderPending   OrderStatus = "pending"
	OrderConfirmed OrderStatus = "confirmed"
	OrderShipped   OrderStatus = "shipped"
)

// LineItem is a single item in an order.
//
//compschema:generate
type LineItem struct {
	SKU string `json:"sku" jsonschema:"pattern=^[A-Z]{3}-[0-9]+$"`
	Qty int    `json:"qty" jsonschema:"minimum=1"`
}

// Order represents a customer order.
//
//compschema:generate
type Order struct {
	ID     string      `json:"id"`
	Items  []LineItem  `json:"items" jsonschema:"minItems=1"`
	Status OrderStatus `json:"status"`
	Notes  *string     `json:"notes,omitempty"`
}

// Shape is a sealed union of shape types.
//
//compschema:generate
type Shape interface {
	isShape()
}

// Circle is a round shape.
type Circle struct {
	Type   string  `json:"type" jsonschema:"const=circle"`
	Radius float64 `json:"radius" jsonschema:"minimum=0"`
}

func (*Circle) isShape() {}

// Rectangle has four sides.
type Rectangle struct {
	Type   string  `json:"type" jsonschema:"const=rectangle"`
	Width  float64 `json:"width" jsonschema:"minimum=0"`
	Height float64 `json:"height" jsonschema:"minimum=0"`
}

func (*Rectangle) isShape() {}
