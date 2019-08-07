package blsmultisig

import (
	"crypto/rand"
	"math/big"
	"testing"
	"time"

	bn256 "github.com/ethereum/go-ethereum/crypto/bn256/google"
)

func testAvgTimeG1P2I(loop int64) int64 {
	sum := int64(0)
	for i := int64(0); i < loop; i++ {
		_, randPoint, _ := bn256.RandomG1(rand.Reader)
		start := time.Now()
		G1P2I(randPoint)
		sum += -(start.Sub(time.Now())).Nanoseconds()
	}
	return sum / loop
}

func testAvgTimeI2G1P(loop int64) int64 {
	sum := int64(0)
	for i := int64(0); i < loop; i++ {
		max := new(big.Int)
		max.Exp(big.NewInt(2), big.NewInt(256), nil).Sub(max, big.NewInt(1))
		randInt, _ := rand.Int(rand.Reader, max)
		start := time.Now()
		I2G1P(randInt)
		sum += -(start.Sub(time.Now())).Nanoseconds()
	}
	return sum / loop
}

func Test_testAvgTimeG1P2I(t *testing.T) {
	type args struct {
		loop int64
	}
	tests := []struct {
		name string
		args args
		want int64
	}{
		{
			name: "Test 10000 loop and UpperBound for function execution time is 0.0001s",
			args: args{
				loop: 10000,
			},
			want: 100000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := testAvgTimeG1P2I(tt.args.loop); got > tt.want {
				t.Errorf("Execution time of testAvgTimeG1P2I() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_testAvgTimeI2G1P(t *testing.T) {
	type args struct {
		loop int64
	}
	tests := []struct {
		name string
		args args
		want int64
	}{
		{
			name: "Test 10000 loop and UpperBound for function execution time is 0.0005s",
			args: args{
				loop: 10000,
			},
			want: 500000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := testAvgTimeI2G1P(tt.args.loop); got > tt.want {
				t.Errorf("Execution time of testAvgTimeI2G1P() = %v, want %v", got, tt.want)
			}
		})
	}
}
