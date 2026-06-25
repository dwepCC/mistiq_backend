package detraccion

import _ "embed"

//go:embed data/goods.json
var goodsJSON []byte

//go:embed data/payment_methods.json
var paymentMethodsJSON []byte
