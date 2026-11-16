// Command atlas-loader renders the database schema of the engine as SQL.
//
// Atlas calls the program as an external schema definition ("diff" in the
// migration block of atlas.hcl) and compares the result with the state of the
// database described by the migration directory.
package main

import (
	"fmt"
	"os"

	"websiteengine/pkg/models"
)

func main() {
	ddl, err := models.SchemaDDL()
	if err != nil {
		fmt.Fprintln(os.Stderr, "atlas-loader:", err)
		os.Exit(1)
	}
	fmt.Print(ddl)
}
