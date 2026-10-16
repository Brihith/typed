// Command users is a small Users API built with typed.
package main

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/Brihith/typed"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type GetUserInput struct {
	ID      int  `path:"id" minimum:"1"`
	Verbose bool `query:"verbose" doc:"Include extra fields"`
}

type CreateUserInput struct {
	Body struct {
		Name  string `json:"name" required:"true" minLength:"1" maxLength:"100"`
		Email string `json:"email" required:"true" format:"email"`
		Role  string `json:"role,omitempty" enum:"admin,member" default:"member"`
	}
}

type DeleteUserInput struct {
	ID int `path:"id" minimum:"1"`
}

type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email" example:"ada@example.com"`
}

var (
	mu     sync.Mutex
	users  = map[int]User{1: {ID: 1, Name: "Ada", Email: "ada@example.com"}}
	nextID = 2
)

func main() {
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	api := typed.New(r, typed.Config{Title: "Users API", Version: "1.0.0"})

	typed.Get(api, "/users/{id}", getUser, typed.Summary("Get a user"), typed.Tags("users"))
	typed.Post(api, "/users", createUser, typed.Status(http.StatusCreated), typed.Tags("users"))

	api.Route("/admin", func(admin *typed.API) {
		admin = admin.With(requireAdmin) // chi middleware
		typed.Delete(admin, "/users/{id}", deleteUser)
	})

	log.Println("docs at http://localhost:8080/docs")
	log.Fatal(http.ListenAndServe(":8080", r)) // also serves /openapi.json and /docs
}

func getUser(ctx context.Context, in *GetUserInput) (*User, error) {
	mu.Lock()
	defer mu.Unlock()
	u, ok := users[in.ID]
	if !ok {
		return nil, typed.NotFound("user not found")
	}
	return &u, nil
}

func createUser(ctx context.Context, in *CreateUserInput) (*User, error) {
	mu.Lock()
	defer mu.Unlock()
	u := User{ID: nextID, Name: in.Body.Name, Email: in.Body.Email}
	nextID++
	users[u.ID] = u
	return &u, nil
}

func deleteUser(ctx context.Context, in *DeleteUserInput) (*struct{}, error) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := users[in.ID]; !ok {
		return nil, typed.NotFound("user not found")
	}
	delete(users, in.ID)
	return &struct{}{}, nil
}

func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin") != "true" {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
