package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

// Quick utility to generate a bcrypt hash for a password
// Usage: go run scripts/fix_user_password.go <password>
func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run scripts/fix_user_password.go <password>")
		fmt.Println("Example: go run scripts/fix_user_password.go 0i2rinbcp12yc31h")
		os.Exit(1)
	}

	password := os.Args[1]

	// Generate bcrypt hash
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Printf("Error generating hash: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Password: %s\n", password)
	fmt.Printf("Bcrypt Hash: %s\n", string(hashedPassword))
	fmt.Printf("\nTo update in MongoDB, run:\n")
	fmt.Printf("db.users.updateOne(\n")
	fmt.Printf("  {\"user.email\": \"morrisjason94@gmail.com\"},\n")
	fmt.Printf("  {$set: {\"user.password\": \"%s\"}}\n", string(hashedPassword))
	fmt.Printf(")\n")
}

