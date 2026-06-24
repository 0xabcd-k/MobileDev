package auth

import "testing"

func Test_Temp(t *testing.T) {
	t.Log(New("114514").expectedToken)
}
