// The shapes a real Postman export takes, and the tolerance the importer needs
// to read them (US-054).
//
// The v2.1 schema is permissive in ways the importer's Go structs were not.
// `request` may be a bare URL string; `header` may be a raw header block;
// `value` may be any scalar; `raw` may be an object; a saved example's `code`
// may arrive as a string. encoding/json aborts the whole document on the first
// mismatch, so one odd request discarded every other request in the file and
// the user was told only that the import "could not be read safely".
//
// Each type here decodes the permissive shape and never fails on a shape
// Postman can write. Failures that remain are reported per item, so a single
// unreadable request costs its neighbours nothing.
package importers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// utf8BOM prefixes files written by Postman on Windows and by several
// export-to-disk integrations. encoding/json rejects it outright.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func trimJSONPrefix(content string) string {
	return strings.TrimSpace(string(bytes.TrimPrefix([]byte(content), utf8BOM)))
}

// postmanFlexString reads any JSON scalar as text. Postman's own UI writes
// numbers and booleans into header and parameter values whenever the user
// pastes them, and `null` whenever a row is cleared without being removed.
type postmanFlexString string

func (value *postmanFlexString) UnmarshalJSON(data []byte) error {
	*value = postmanFlexString(postmanScalarText(data))
	return nil
}

func (value postmanFlexString) String() string { return string(value) }

// postmanScalarText renders a JSON scalar the way a user typed it, and leaves
// composites as their compact JSON so nothing is silently discarded.
func postmanScalarText(data []byte) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return text
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err == nil {
		return number.String()
	}
	var flag bool
	if err := json.Unmarshal(trimmed, &flag); err == nil {
		return strconv.FormatBool(flag)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, trimmed, "", "  "); err == nil {
		return pretty.String()
	}
	return string(trimmed)
}

// postmanFlexInt reads a saved example's status code, which Postman writes as a
// number but which several exporters and hand-edited collections carry as text.
type postmanFlexInt int

func (value *postmanFlexInt) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var number int
	if err := json.Unmarshal(trimmed, &number); err == nil {
		*value = postmanFlexInt(number)
		return nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		if parsed, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
			*value = postmanFlexInt(parsed)
		}
	}
	return nil
}

func (value postmanFlexInt) Int() int { return int(value) }

// postmanHeaderList reads the three shapes a header block takes: the list of
// objects the schema documents, a raw header block as one string (what the
// "bulk edit" view produces), or a list of such strings.
type postmanHeaderList []postmanHeader

func (list *postmanHeaderList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(trimmed, &rows); err == nil {
		parsed := make(postmanHeaderList, 0, len(rows))
		for _, row := range rows {
			var header postmanHeader
			if err := json.Unmarshal(row, &header); err == nil {
				parsed = append(parsed, header)
				continue
			}
			parsed = append(parsed, parsePostmanHeaderBlock(postmanScalarText(row))...)
		}
		*list = parsed
		return nil
	}
	var block string
	if err := json.Unmarshal(trimmed, &block); err == nil {
		*list = parsePostmanHeaderBlock(block)
		return nil
	}
	return nil
}

// parsePostmanHeaderBlock reads a wire-format header block. A line without a
// colon is not a header and is dropped rather than imported as a nameless row.
//
// A leading "//" marks the row DISABLED rather than commenting it out. That is
// how Postman's bulk-edit view spells an unticked header, so treating the
// marker as part of the name imported a header literally called
// "//Content-Type" -- enabled, and sent on the wire. Dropping the line instead
// would lose a row the user can see in the UI they exported from; a disabled
// row is part of the request, not an absent one, which is the same rule the
// .bru writer follows.
func parsePostmanHeaderBlock(block string) postmanHeaderList {
	if strings.TrimSpace(block) == "" {
		return nil
	}
	rows := postmanHeaderList{}
	for _, line := range strings.Split(strings.ReplaceAll(block, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Checked against the trimmed LINE, so a "//" inside a value -- the
		// scheme in "Referer: https://example.test" -- is untouched.
		disabled := false
		if rest, marked := strings.CutPrefix(trimmed, "//"); marked {
			disabled = true
			trimmed = strings.TrimSpace(rest)
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found || strings.TrimSpace(key) == "" {
			continue
		}
		rows = append(rows, postmanHeader{
			Key:      postmanFlexString(strings.TrimSpace(key)),
			Value:    postmanFlexString(strings.TrimSpace(value)),
			Disabled: disabled,
		})
	}
	return rows
}

// postmanRequestRef reads a request that is either the documented object or the
// bare URL string the schema also allows ("request": "https://example.test").
type postmanRequestRef struct {
	request *postmanRequest
}

func (ref *postmanRequestRef) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var shorthand string
	if err := json.Unmarshal(trimmed, &shorthand); err == nil {
		ref.request = &postmanRequest{URL: shorthand}
		return nil
	}
	var request postmanRequest
	if err := json.Unmarshal(trimmed, &request); err != nil {
		return err
	}
	ref.request = &request
	return nil
}

func (ref postmanRequestRef) Request() *postmanRequest { return ref.request }

// postmanItemList decodes each entry on its own so one unreadable request costs
// only itself. The schema says `item` is an array; a single object is accepted
// too because hand-written and script-generated collections carry one.
type postmanItemList struct {
	Items    []postmanItem
	Warnings []string
}

func (list *postmanItemList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	rows := []json.RawMessage{}
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		if !bytes.HasPrefix(trimmed, []byte("{")) {
			return err
		}
		rows = []json.RawMessage{trimmed}
	}
	for index, row := range rows {
		var item postmanItem
		if err := json.Unmarshal(row, &item); err != nil {
			list.Warnings = append(list.Warnings, postmanItemWarning(row, index, err))
			continue
		}
		list.Items = append(list.Items, item)
	}
	return nil
}

// postmanItemWarning names the entry the user has to go and look at. The name
// is read on its own because it is usually readable even when the rest is not.
func postmanItemWarning(row json.RawMessage, index int, err error) string {
	var probe struct {
		Name postmanFlexString `json:"name"`
	}
	_ = json.Unmarshal(row, &probe)
	label := strings.TrimSpace(probe.Name.String())
	if label == "" {
		label = "entry " + strconv.Itoa(index+1)
	}
	return "skipped " + strconv.Quote(label) + ": " + describePostmanJSONError(err)
}

// describePostmanJSONError turns encoding/json's wording into the field and
// value the person editing the collection can act on.
func describePostmanJSONError(err error) string {
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		field := strings.TrimPrefix(typeError.Field, ".")
		if field == "" {
			field = "value"
		}
		return "field " + strconv.Quote(field) + " is a " + typeError.Value + " where a " + typeError.Type.String() + " is required"
	}
	return err.Error()
}

// PostmanJSONError restates an encoding/json failure with the line and column a
// person can navigate to. json.SyntaxError reports a byte offset, which is of
// no use to anyone looking at the file in an editor.
//
// US-055. The alternative -- reporting only "could not be read safely" -- sent
// every malformed-collection question to the console of a packaged desktop
// build, where there is no console.
func PostmanJSONError(content string, err error) error {
	if err == nil {
		return nil
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		line, column := jsonPosition(content, syntaxError.Offset)
		return fmt.Errorf("invalid JSON at line %d, column %d: %s", line, column, syntaxError.Error())
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		field := strings.TrimPrefix(typeError.Field, ".")
		if field == "" {
			field = "value"
		}
		line, column := jsonPosition(content, typeError.Offset)
		return fmt.Errorf("field %q is a %s where a %s is required, at line %d, column %d", field, typeError.Value, typeError.Type.String(), line, column)
	}
	return err
}

// jsonPosition converts a byte offset into the 1-based line and column of the
// original document, counting runes so a column in a file with non-ASCII text
// matches what an editor shows.
func jsonPosition(content string, offset int64) (int, int) {
	if offset < 0 {
		return 1, 1
	}
	if offset > int64(len(content)) {
		offset = int64(len(content))
	}
	line, column := 1, 1
	for index, symbol := range content {
		if int64(index) >= offset {
			break
		}
		if symbol == '\n' {
			line, column = line+1, 1
			continue
		}
		column++
	}
	return line, column
}
