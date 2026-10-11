package menu

import (
	"fmt"
	"strconv"
	"strings"
)

type State struct {
	Items []string
	index int
}

func New(items []string) *State {
	return &State{Items: items}
}

func posmod(a, b int) int {
	return ((a % b) + b) % b
}

func (m *State) Up() string {
	m.index = posmod(m.index-1, len(m.Items))
	return m.Current()
}

func (m *State) Down() string {
	m.index = posmod(m.index+1, len(m.Items))
	return m.Current()
}

func (m *State) Pick(number string) (string, bool) {
	i, err := strconv.Atoi(number)
	if err != nil || i < 1 || i > len(m.Items) {
		return "", false
	}
	m.index = i - 1
	return m.Current(), true
}

func (m *State) Current() string {
	return m.Items[m.index]
}

func (m *State) Index() int {
	return m.index
}

func (m *State) Render() string {
	var sb strings.Builder
	for i, item := range m.Items {
		marker := " "
		if i == m.index {
			marker = ">"
		}
		fmt.Fprintf(&sb, "%s %d. %s\n", marker, i+1, item)
	}
	return strings.TrimRight(sb.String(), "\n")
}
