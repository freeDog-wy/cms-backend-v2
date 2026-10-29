package repo

import "gorm.io/gorm"

// One 将零到一条查询结果转换为单对象查询约定。
func One[T any](rows []T, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	return &rows[0], nil
}
