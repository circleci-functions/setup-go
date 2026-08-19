// main.go
package main

import (
	"fmt"

	"circleci.com/go-fuction-tests/numbers"
	"circleci.com/go-fuction-tests/words"
)

func main() {
	word := words.Word()
	number := numbers.Number()
	fmt.Printf("%s %d\n", word, number)
}
