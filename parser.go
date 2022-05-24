package main

import (
	"container/list"
	"html/template"
	"math"
	"strconv"
	"strings"
)

type stack[T any] struct {
	l *list.List
}

func newStack[T any]() *stack[T] {
	return &stack[T]{l: list.New()}
}

func (s *stack[T]) push(x T) {
	s.l.PushBack(x)
}

func (s *stack[T]) peek() T {
	if s.l.Len() == 0 {
		var ret T
		return ret
	}
	return s.l.Back().Value.(T)
}

func (s *stack[T]) pop() T {
	if s.l.Len() == 0 {
		var ret T
		return ret
	}
	tail := s.l.Back()
	return s.l.Remove(tail).(T)
}

func parseDocument(document string) (html template.HTML) {
	content := []rune("\n" + document) // The newline simplifies finding tokens which are at the start of a line.
	length := len(content)
	var i int // Current index
	var out string

	element := newStack[string]()
	linkStart := 0     // Stores the index of the start of the text between a link's braces.
	headingLevel := "" // Stores the last added heading, e.g. `h1`.
	// TODO (nested): listLevel := newStack[int]()

	// match checks if there is an occurrence of substr at the current index of input.
	match := func(substr string) bool {
		if i+len(substr) > length {
			return false
		}
		return string(content[i:i+len(substr)]) == (substr)
	}

	// countConsecutive counts consecutive occurrences of substr at the current index of input.
	countConsecutive := func(substr string) int {
		count := 0
		for match(substr) {
			count++
			i += len(substr)
		}
		i -= len(substr) * count
		return count
	}

	for i = 0; i < length; i++ {
		if match("\n") {
			switch element.peek() {
			case "h": // End of a heading
				element.pop()
				out += "</" + headingLevel + ">"
				break
			case "blockquote":
				if !match("\n> ") { // End of a block quote
					element.pop()
					out += "</blockquote>"
				} else { // New line in a block quote
					out += "\n"
					i += 2
					continue
				}
				break
			case "ul":
				if !match("\n- ") { // End of the list
					element.pop()
					out += "</li></ul>"
				} else { // New bullet point
					out += "</li><li>"
					i += 2
					continue
				}
				break
			case "ol":
				if !match("\n. ") { // End of the list
					element.pop()
					out += "</li></ol>"
				} else { // New list element
					out += "</li><li>"
					i += 2
					continue
				}
				break
			default: // New line
				out += "\n"
				break
			}
		}
		if element.peek() == "{}" && !match("}") {
			// The current element is a link, and the current character doesn't close it.
			continue
		}
		if match("\n```") { // Code block
			if element.peek() == "```" { // End
				element.pop()
				out += "</pre>\b" // Refer to the comment at the last loop in this function.
			} else { // Beginning
				element.push("```")
				if out[len(out)-3:len(out)-1] == "\b\n" {
					// Deletes the excess newline between two consecutive
					// code blocks without an empty line between them.
					out = out[:len(out)-2]
				}
				out += "<pre>"
			}
			i += 3
			continue
		}
		if element.peek() == "```" { // Code block content
			if !match("\n") {
				out += string(content[i])
			}
			continue
		}
		if match("`") { // Inline code
			if element.peek() == "`" { // End
				element.pop()
				out += "</code>"
			} else { // Beginning
				element.push("`")
				out += "<code>"
			}
			continue
		}
		if element.peek() == "`" { // Inline code content
			if !match("\n") {
				out += string(content[i])
			}
			continue
		}
		if match("{") { // Beginning of a link
			element.push("{}")
			linkStart = i + 1
			continue
		}
		if match("}") { // End of a link
			element.pop()
			link := content[linkStart:i]
			sliced := strings.SplitN(string(link), " ", 2)
			sliced = append(sliced, sliced[0])
			out += `<a href="` + sliced[0] + `">` + sliced[1] + "</a>"
			continue
		}
		if match("\n#") { // Heading
			i++
			num := countConsecutive("#")
			i--
			if content[i+1+num] == ' ' {
				headingLevel = "h" + strconv.Itoa(int(math.Min(float64(num), 6)))
				element.push("h")
				out += "<" + headingLevel + ">"
				i += num + 1
				continue
			}
		}
		if match("\n> ") { // Block quote beginning
			element.push("blockquote")
			out += "<blockquote>"
			i += 2
			continue
		}
		if match("\n- ") { // Unordered list
			element.push("ul")
			out += "<ul><li>"
			i += 2
			continue
		}
		if match("\n. ") { // Ordered list
			element.push("ol")
			out += "<ol><li>"
			i += 2
			continue
		}
		if match("\n---") { // Horizontal rule
			out += "<hr>"
			i += 3
			continue
		}
		if !match("\n") { // Character copied literally
			out += string(content[i])
		}
	}

	var outRunes []rune
	if out[0] == '\n' {
		// Delete the newline added in the beginning of the function.
		outRunes = []rune(out[1:])
	} else {
		outRunes = []rune(out)
	}
	length = len(outRunes)
	for j := 0; j < length; j++ {
		// The backspace character is used for marking potential newlines
		// to delete in order to preserve the expected newline count.
		// In contrast to the normal use of a backspace, this one is used
		// to delete the character *after* it.
		if outRunes[j] == '\b' {
			if j+1 < length && outRunes[j+1] == '\n' {
				j++
			}
		} else {
			html += template.HTML(outRunes[j])
		}
	}

	return
}
