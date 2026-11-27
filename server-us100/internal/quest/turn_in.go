package quest

import "dfolan/internal/inventory"

// Only loose bag contents can be handed in. Make a private copy so a later
// missing ingredient or a full reward bag cannot partly consume the inputs.
func takeItems(b inventory.Bag, needs []ItemNeed) (inventory.Bag, bool) {
	b.Items = append([]inventory.BagItem(nil), b.Items...)
	b.Equipment = append([]inventory.BagEquipment(nil), b.Equipment...)
	for _, need := range needs {
		if need.Template == 0 {
			if b.Gold < need.Amount {
				return inventory.Bag{}, false
			}
			b.Gold -= need.Amount
			continue
		}
		left := need.Amount
		stacks := b.Items[:0]
		for _, v := range b.Items {
			if v.Template == need.Template {
				n := min(left, v.Amount)
				left -= n
				v.Amount -= n
			}
			if v.Amount > 0 {
				stacks = append(stacks, v)
			}
		}
		b.Items = stacks
		gear := b.Equipment[:0]
		for _, v := range b.Equipment {
			if left > 0 && v.Template == need.Template {
				left--
				continue
			}
			gear = append(gear, v)
		}
		b.Equipment = gear
		if left != 0 {
			return inventory.Bag{}, false
		}
	}
	return b, len(needs) > 0
}
