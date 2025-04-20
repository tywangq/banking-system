package util

import (
	"math/rand"
	"time"
)

var rnd = rand.New(rand.NewSource(time.Now().UnixNano()))

const alphabet = "abcdefghijklmnopqrstuvwxyz"

// func init() {
// 	rand.Seed(time.Now().UnixNano())
// }

func RandomInt(min, max int64) int64 {
	return min + rnd.Int63n(max-min+1) // min + [0, max-min+1) => [min, max]
}

func RandomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rnd.Intn(len(alphabet))]
	}
	return string(b)
}

func RandomOwner() string {
	return RandomString(6)
}

func RandomMoney() int64 {
	return RandomInt(0, 1000)
}

func RandomCurrency() string {
	currencies := []string{USD, EUR, CAD}
	n := len(currencies)
	return currencies[RandomInt(0, int64(n)-1)]
}

func RandomEmail() string {
	return RandomString(6) + "@" + RandomString(6) + ".com"
}
