// Package graphqltest serves a small GraphQL shop over HTTP, for the
// GraphQL module's tests and the demo API (ADR-021). It uses a real
// GraphQL executor (github.com/graphql-go/graphql), so validation,
// variables, partial data and errors follow the GraphQL specification.
//
//	type Query {
//	  products: [Product!]!
//	  product(id: Int!): Product        # an error for an unknown id
//	  fail(message: String): String     # always an error
//	}
//	type Mutation {
//	  placeOrder(productId: Int!, quantity: Int!): Order   # quantity 1 to 100
//	}
package graphqltest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/graphql-go/graphql"
)

type product struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	PriceCents int    `json:"priceCents"`
}

var products = []product{
	{1, "Espresso beans, 1 kg", 2490},
	{2, "Milk frother", 3900},
	{3, "Ceramic cup", 1200},
	{4, "Hand grinder", 5400},
	{5, "Paper filters (100)", 450},
}

func findProduct(id int) (product, bool) {
	for _, p := range products {
		if p.ID == id {
			return p, true
		}
	}
	return product{}, false
}

// Handler serves GraphQL at any path: a POST with a JSON body of
// {query, variables, operationName}. An executed operation answers 200,
// with "errors" when it failed (the usual GraphQL-over-HTTP behaviour);
// a body that is not a GraphQL request answers 400. Each operation waits
// delay first.
func Handler(delay time.Duration) (http.Handler, error) {
	var orders atomic.Int64
	schema, err := newSchema(&orders)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSON(w, http.StatusMethodNotAllowed, errorsBody("use POST with a JSON body"))
			return
		}
		var req struct {
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || req.Query == "" {
			writeJSON(w, http.StatusBadRequest, errorsBody("the body must be JSON with a query"))
			return
		}
		if delay > 0 {
			t := time.NewTimer(delay)
			select {
			case <-t.C:
			case <-r.Context().Done():
				t.Stop()
				return
			}
		}
		res := graphql.Do(graphql.Params{
			Schema: schema, RequestString: req.Query, VariableValues: req.Variables,
			OperationName: req.OperationName, Context: r.Context(),
		})
		writeJSON(w, http.StatusOK, res)
	}), nil
}

func errorsBody(msg string) map[string]any {
	return map[string]any{"errors": []map[string]string{{"message": msg}}}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/graphql-response+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newSchema(orders *atomic.Int64) (graphql.Schema, error) {
	productType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Product",
		Fields: graphql.Fields{
			"id":         {Type: graphql.NewNonNull(graphql.Int)},
			"name":       {Type: graphql.NewNonNull(graphql.String)},
			"priceCents": {Type: graphql.NewNonNull(graphql.Int)},
		},
	})
	orderType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Order",
		Fields: graphql.Fields{
			"id":         {Type: graphql.NewNonNull(graphql.Int)},
			"productId":  {Type: graphql.NewNonNull(graphql.Int)},
			"quantity":   {Type: graphql.NewNonNull(graphql.Int)},
			"totalCents": {Type: graphql.NewNonNull(graphql.Int)},
		},
	})
	query := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"products": {
				Type:    graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(productType))),
				Resolve: func(graphql.ResolveParams) (any, error) { return products, nil },
			},
			"product": {
				Type: productType,
				Args: graphql.FieldConfigArgument{"id": {Type: graphql.NewNonNull(graphql.Int)}},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					id := p.Args["id"].(int)
					if pr, ok := findProduct(id); ok {
						return pr, nil
					}
					return nil, fmt.Errorf("no product with id %d", id)
				},
			},
			"fail": {
				Type: graphql.String,
				Args: graphql.FieldConfigArgument{"message": {Type: graphql.String}},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					msg, _ := p.Args["message"].(string)
					if msg == "" {
						msg = "failed as asked"
					}
					return nil, errors.New(msg)
				},
			},
		},
	})
	mutation := graphql.NewObject(graphql.ObjectConfig{
		Name: "Mutation",
		Fields: graphql.Fields{
			"placeOrder": {
				Type: orderType,
				Args: graphql.FieldConfigArgument{
					"productId": {Type: graphql.NewNonNull(graphql.Int)},
					"quantity":  {Type: graphql.NewNonNull(graphql.Int)},
				},
				Resolve: func(p graphql.ResolveParams) (any, error) {
					id, qty := p.Args["productId"].(int), p.Args["quantity"].(int)
					pr, ok := findProduct(id)
					if !ok {
						return nil, fmt.Errorf("no product with id %d", id)
					}
					if qty < 1 || qty > 100 {
						return nil, errors.New("quantity must be 1 to 100")
					}
					return map[string]any{"id": int(orders.Add(1)), "productId": id, "quantity": qty, "totalCents": pr.PriceCents * qty}, nil
				},
			},
		},
	})
	return graphql.NewSchema(graphql.SchemaConfig{Query: query, Mutation: mutation})
}
