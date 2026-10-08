package premium

// Context variants remain stable between refreshes of the same screen.
var contextVariants = map[string][3]string{
	"home":     {"5416041192905265756", "5465226866321268133", "5465226866321268133"},
	"back":     {"5258236805890710909", "5386340832628462681", "5258132936401624790"},
	"phone":    {"5407025283456835913", "5332531536723984111", "5800762196954716357"},
	"chart":    {"5231200819986047254", "5244837092042750681", "5258391025281408576"},
	"settings": {"5341715473882955310", "5366231924597604153", "5341715473882955310"},
}

func ContextEmojiID(id, context string) string {
	index := 0
	if context == "admin" {
		index = 1
	} else if context == "detail" {
		index = 2
	}
	for role, variants := range contextVariants {
		if id == ID(role) {
			return variants[index]
		}
	}
	return id
}
