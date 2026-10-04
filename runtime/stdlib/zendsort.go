package stdlib

// zendSort reorders positions through the same insertion/quicksort hybrid the
// linked Zend engine uses. Comparator call order is observable when the
// comparator is PHP code, so using Go's sort package is not equivalent even
// when both algorithms produce the same final array.
func zendSort(length int, compare func(int, int) (int, error), swap func(int, int)) error {
	var sortRange func(int, int) error
	var sort2 func(int, int) error
	var sort3 func(int, int, int) error
	var sort4 func(int, int, int, int) error
	var sort5 func(int, int, int, int, int) error

	sort2 = func(a, b int) error {
		order, err := compare(a, b)
		if err != nil {
			return err
		}
		if order > 0 {
			swap(a, b)
		}
		return nil
	}
	sort3 = func(a, b, c int) error {
		order, err := compare(a, b)
		if err != nil {
			return err
		}
		if order <= 0 {
			order, err = compare(b, c)
			if err != nil {
				return err
			}
			if order <= 0 {
				return nil
			}
			swap(b, c)
			order, err = compare(a, b)
			if err != nil {
				return err
			}
			if order > 0 {
				swap(a, b)
			}
			return nil
		}
		order, err = compare(c, b)
		if err != nil {
			return err
		}
		if order <= 0 {
			swap(a, c)
			return nil
		}
		swap(a, b)
		order, err = compare(b, c)
		if err != nil {
			return err
		}
		if order > 0 {
			swap(b, c)
		}
		return nil
	}
	sort4 = func(a, b, c, d int) error {
		if err := sort3(a, b, c); err != nil {
			return err
		}
		order, err := compare(c, d)
		if err != nil || order <= 0 {
			return err
		}
		swap(c, d)
		order, err = compare(b, c)
		if err != nil || order <= 0 {
			return err
		}
		swap(b, c)
		order, err = compare(a, b)
		if err != nil {
			return err
		}
		if order > 0 {
			swap(a, b)
		}
		return nil
	}
	sort5 = func(a, b, c, d, e int) error {
		if err := sort4(a, b, c, d); err != nil {
			return err
		}
		order, err := compare(d, e)
		if err != nil || order <= 0 {
			return err
		}
		swap(d, e)
		order, err = compare(c, d)
		if err != nil || order <= 0 {
			return err
		}
		swap(c, d)
		order, err = compare(b, c)
		if err != nil || order <= 0 {
			return err
		}
		swap(b, c)
		order, err = compare(a, b)
		if err != nil {
			return err
		}
		if order > 0 {
			swap(a, b)
		}
		return nil
	}

	insert := func(base, count int) error {
		switch count {
		case 0, 1:
			return nil
		case 2:
			return sort2(base, base+1)
		case 3:
			return sort3(base, base+1, base+2)
		case 4:
			return sort4(base, base+1, base+2, base+3)
		case 5:
			return sort5(base, base+1, base+2, base+3, base+4)
		}
		for i := base + 1; i < base+6; i++ {
			j := i - 1
			order, err := compare(j, i)
			if err != nil {
				return err
			}
			if order <= 0 {
				continue
			}
			for j != base {
				j--
				order, err = compare(j, i)
				if err != nil {
					return err
				}
				if order <= 0 {
					j++
					break
				}
			}
			for k := i; k > j; k-- {
				swap(k, k-1)
			}
		}
		for i := base + 6; i < base+count; i++ {
			j := i - 1
			order, err := compare(j, i)
			if err != nil {
				return err
			}
			if order <= 0 {
				continue
			}
			for {
				j -= 2
				order, err = compare(j, i)
				if err != nil {
					return err
				}
				if order <= 0 {
					j++
					order, err = compare(j, i)
					if err != nil {
						return err
					}
					if order <= 0 {
						j++
					}
					break
				}
				if j == base {
					break
				}
				if j == base+1 {
					j--
					order, err = compare(i, j)
					if err != nil {
						return err
					}
					if order > 0 {
						j++
					}
					break
				}
			}
			for k := i; k > j; k-- {
				swap(k, k-1)
			}
		}
		return nil
	}

	sortRange = func(base, count int) error {
		for {
			if count <= 16 {
				return insert(base, count)
			}
			end := base + count
			offset := count >> 1
			pivot := base + offset
			if count>>10 != 0 {
				delta := offset >> 1
				if err := sort5(base, base+delta, pivot, pivot+delta, end-1); err != nil {
					return err
				}
			} else if err := sort3(base, pivot, end-1); err != nil {
				return err
			}
			swap(base+1, pivot)
			pivot = base + 1
			i, j := pivot+1, end-1
			for {
				order, err := compare(pivot, i)
				if err != nil {
					return err
				}
				for order > 0 {
					i++
					if i == j {
						break
					}
					order, err = compare(pivot, i)
					if err != nil {
						return err
					}
				}
				if i == j {
					break
				}
				j--
				if j == i {
					break
				}
				order, err = compare(j, pivot)
				if err != nil {
					return err
				}
				for order > 0 {
					j--
					if j == i {
						break
					}
					order, err = compare(j, pivot)
					if err != nil {
						return err
					}
				}
				if j == i {
					break
				}
				swap(i, j)
				i++
				if i == j {
					break
				}
			}
			swap(pivot, i-1)
			left, right := i-base-1, end-i
			if left < right {
				if err := sortRange(base, left); err != nil {
					return err
				}
				base, count = i, right
			} else {
				if err := sortRange(i, right); err != nil {
					return err
				}
				count = left
			}
		}
	}
	return sortRange(0, length)
}
