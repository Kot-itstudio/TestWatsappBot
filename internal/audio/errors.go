package audio

import "errors"

var (
	errBadExt = errors.New("недопустимое расширение файла")
	errEscape = errors.New("выход за пределы папки audio")
)
