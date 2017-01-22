package mapping

import (
	"errors"
	"fmt"
	"io/ioutil"
	"regexp"

	"github.com/omniscale/imposm3/element"

	"gopkg.in/yaml.v2"
)

type Field struct {
	Name       string                 `yaml:"name"`
	Key        Key                    `yaml:"key"`
	Keys       []Key                  `yaml:"keys"`
	Type       string                 `yaml:"type"`
	Args       map[string]interface{} `yaml:"args"`
	FromMember bool                   `yaml:"from_member"`
}

type Table struct {
	Name         string
	Type         TableType             `yaml:"type"`
	Mapping      KeyValues             `yaml:"mapping"`
	Mappings     map[string]SubMapping `yaml:"mappings"`
	TypeMappings TypeMappings          `yaml:"type_mappings"`
	Fields       []*Field              `yaml:"columns"` // TODO rename Fields internaly to Columns
	OldFields    []*Field              `yaml:"fields"`
	Filters      *Filters              `yaml:"filters"`
}

type GeneralizedTable struct {
	Name            string
	SourceTableName string  `yaml:"source"`
	Tolerance       float64 `yaml:"tolerance"`
	SqlFilter       string  `yaml:"sql_filter"`
}

type Filters struct {
	ExcludeTags   *[][]string    `yaml:"exclude_tags"`
	Reject        KeyValues      `yaml:"reject"`
	Require       KeyValues      `yaml:"require"`
	RejectRegexp  KeyRegexpValue `yaml:"reject_regexp"`
	RequireRegexp KeyRegexpValue `yaml:"require_regexp"`
}

type Tables map[string]*Table

type GeneralizedTables map[string]*GeneralizedTable

type Mapping struct {
	Tables            Tables            `yaml:"tables"`
	GeneralizedTables GeneralizedTables `yaml:"generalized_tables"`
	Tags              Tags              `yaml:"tags"`
	Areas             Areas             `yaml:"areas"`
	// SingleIdSpace mangles the overlapping node/way/relation IDs
	// to be unique (nodes positive, ways negative, relations negative -1e17)
	SingleIdSpace bool `yaml:"use_single_id_space"`
}

type Areas struct {
	AreaTags   []Key `yaml:"area_tags"`
	LinearTags []Key `yaml:"linear_tags"`
}

type Tags struct {
	LoadAll bool  `yaml:"load_all"`
	Exclude []Key `yaml:"exclude"`
	Include []Key `yaml:"include"`
}

type orderedValue struct {
	value Value
	order int
}
type KeyValues map[Key][]orderedValue

type KeyRegexpValue map[Key]string

func (kv *KeyValues) UnmarshalYAML(unmarshal func(interface{}) error) error {
	if *kv == nil {
		*kv = make(map[Key][]orderedValue)
	}
	slice := yaml.MapSlice{}
	err := unmarshal(&slice)
	if err != nil {
		return err
	}
	order := 0
	for _, item := range slice {
		k, ok := item.Key.(string)
		if !ok {
			return fmt.Errorf("mapping key '%s' not a string", k)
		}
		values, ok := item.Value.([]interface{})
		if !ok {
			return fmt.Errorf("mapping key '%s' not a string", k)
		}
		for _, v := range values {
			if v, ok := v.(string); ok {
				(*kv)[Key(k)] = append((*kv)[Key(k)], orderedValue{value: Value(v), order: order})
			} else {
				return fmt.Errorf("mapping value '%s' not a string", v)
			}
			order += 1
		}
	}
	return nil
}

type SubMapping struct {
	Mapping KeyValues
}

type TypeMappings struct {
	Points      KeyValues `yaml:"points"`
	LineStrings KeyValues `yaml:"linestrings"`
	Polygons    KeyValues `yaml:"polygons"`
}

type ElementFilter func(tags element.Tags, key Key, closed bool) bool

type TagTables map[Key]map[Value][]OrderedDestTable

type DestTable struct {
	Name       string
	SubMapping string
}

type OrderedDestTable struct {
	DestTable
	order int
}

type TableType string

func (tt *TableType) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "":
		return errors.New("missing table type")
	case `"point"`:
		*tt = PointTable
	case `"linestring"`:
		*tt = LineStringTable
	case `"polygon"`:
		*tt = PolygonTable
	case `"geometry"`:
		*tt = GeometryTable
	case `"relation"`:
		*tt = RelationTable
	case `"relation_member"`:
		*tt = RelationMemberTable
	default:
		return errors.New("unknown type " + string(data))
	}
	return nil
}

const (
	PolygonTable        TableType = "polygon"
	LineStringTable     TableType = "linestring"
	PointTable          TableType = "point"
	GeometryTable       TableType = "geometry"
	RelationTable       TableType = "relation"
	RelationMemberTable TableType = "relation_member"
)

func NewMapping(filename string) (*Mapping, error) {
	f, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	mapping := Mapping{}
	err = yaml.Unmarshal(f, &mapping)
	if err != nil {
		return nil, err
	}

	err = mapping.prepare()
	if err != nil {
		return nil, err
	}
	return &mapping, nil
}

func (t *Table) ExtraTags() map[Key]bool {
	tags := make(map[Key]bool)
	for _, field := range t.Fields {
		if field.Key != "" {
			tags[field.Key] = true
		}
		for _, k := range field.Keys {
			tags[k] = true
		}
	}
	return tags
}

func (m *Mapping) prepare() error {
	for name, t := range m.Tables {
		t.Name = name
		if t.OldFields != nil {
			// todo deprecate 'fields'
			t.Fields = t.OldFields
		}
	}

	for name, t := range m.GeneralizedTables {
		t.Name = name
	}
	return nil
}

func (tt TagTables) addFromMapping(mapping KeyValues, table DestTable) {
	for key, vals := range mapping {
		for _, v := range vals {
			vals, ok := tt[key]
			tbl := OrderedDestTable{DestTable: table, order: v.order}
			if ok {
				vals[v.value] = append(vals[v.value], tbl)
			} else {
				tt[key] = make(map[Value][]OrderedDestTable)
				tt[key][v.value] = append(tt[key][v.value], tbl)
			}
		}
	}
}

func (m *Mapping) mappings(tableType TableType, mappings TagTables) {
	for name, t := range m.Tables {
		if t.Type != GeometryTable && t.Type != tableType {
			continue
		}
		mappings.addFromMapping(t.Mapping, DestTable{Name: name})

		for subMappingName, subMapping := range t.Mappings {
			mappings.addFromMapping(subMapping.Mapping, DestTable{Name: name, SubMapping: subMappingName})
		}

		switch tableType {
		case PointTable:
			mappings.addFromMapping(t.TypeMappings.Points, DestTable{Name: name})
		case LineStringTable:
			mappings.addFromMapping(t.TypeMappings.LineStrings, DestTable{Name: name})
		case PolygonTable:
			mappings.addFromMapping(t.TypeMappings.Polygons, DestTable{Name: name})
		}
	}
}

func (m *Mapping) tables(tableType TableType) map[string]*TableFields {
	result := make(map[string]*TableFields)
	for name, t := range m.Tables {
		if t.Type == tableType || t.Type == "geometry" {
			result[name] = t.TableFields()
		}
	}
	return result
}

func (m *Mapping) extraTags(tableType TableType, tags map[Key]bool) {
	for _, t := range m.Tables {
		if t.Type != tableType && t.Type != "geometry" {
			continue
		}
		for key, _ := range t.ExtraTags() {
			tags[key] = true
		}
		if t.Filters != nil && t.Filters.ExcludeTags != nil {
			for _, keyVal := range *t.Filters.ExcludeTags {
				tags[Key(keyVal[0])] = true
			}
		}
	}
	for _, k := range m.Tags.Include {
		tags[k] = true
	}

	// always include area tag for closed-way handling
	tags["area"] = true
}

func (m *Mapping) ElementFilters() map[string][]ElementFilter {
	result := make(map[string][]ElementFilter)

	var areaTags map[Key]struct{}
	var linearTags map[Key]struct{}
	if m.Areas.AreaTags != nil {
		areaTags = make(map[Key]struct{})
		for _, tag := range m.Areas.AreaTags {
			areaTags[tag] = struct{}{}
		}
	}
	if m.Areas.LinearTags != nil {
		linearTags = make(map[Key]struct{})
		for _, tag := range m.Areas.LinearTags {
			linearTags[tag] = struct{}{}
		}
	}

	for name, t := range m.Tables {
		if t.Type == LineStringTable && areaTags != nil {
			f := func(tags element.Tags, key Key, closed bool) bool {
				if closed {
					if tags["area"] == "yes" {
						return false
					}
					if tags["area"] != "no" {
						if _, ok := areaTags[key]; ok {
							return false
						}
					}
				}
				return true
			}
			result[name] = append(result[name], f)
		}
		if t.Type == PolygonTable && linearTags != nil {
			f := func(tags element.Tags, key Key, closed bool) bool {
				if closed && tags["area"] == "no" {
					return false
				}
				if tags["area"] != "yes" {
					if _, ok := linearTags[key]; ok {
						return false
					}
				}
				return true
			}
			result[name] = append(result[name], f)
		}

		if t.Filters == nil {
			continue
		}
		if t.Filters.ExcludeTags != nil {
			log.Print("warn: exclude_tags filter is deprecated and will be removed. See require and reject filter.")
			for _, filterKeyVal := range *t.Filters.ExcludeTags {
				// Convert `exclude_tags`` filter to `reject` filter !
				keyname := string(filterKeyVal[0])
				vararr := []orderedValue{
					{
						value: Value(filterKeyVal[1]),
						order: 1,
					},
				}
				result[name] = append(result[name], makeFiltersFunction(name, false, true, string(keyname), vararr))
			}
		}

		if t.Filters.Require != nil {
			for keyname, vararr := range t.Filters.Require {
				result[name] = append(result[name], makeFiltersFunction(name, true, false, string(keyname), vararr))
			}
		}

		if t.Filters.Reject != nil {
			for keyname, vararr := range t.Filters.Reject {
				result[name] = append(result[name], makeFiltersFunction(name, false, true, string(keyname), vararr))
			}
		}

		if t.Filters.RequireRegexp != nil {
			for keyname, regexp := range t.Filters.RequireRegexp {
				result[name] = append(result[name], makeRegexpFiltersFunction(name, true, false, string(keyname), regexp))
			}
		}

		if t.Filters.RejectRegexp != nil {
			for keyname, regexp := range t.Filters.RejectRegexp {
				result[name] = append(result[name], makeRegexpFiltersFunction(name, false, true, string(keyname), regexp))
			}
		}

	}
	return result
}

func findValueInOrderedValue(v Value, list []orderedValue) bool {
	for _, item := range list {
		if item.value == v {
			return true
		}
	}
	return false
}

func makeRegexpFiltersFunction(tablename string, virtualTrue bool, virtualFalse bool, v_keyname string, v_regexp string) func(tags element.Tags, key Key, closed bool) bool {
	// Compile regular expression,  if not valid regexp --> panic !
	r := regexp.MustCompile(v_regexp)
	return func(tags element.Tags, key Key, closed bool) bool {
		if v, ok := tags[v_keyname]; ok {
			if r.MatchString(v) {
				return virtualTrue
			}
		}
		return virtualFalse
	}
}

func makeFiltersFunction(tablename string, virtualTrue bool, virtualFalse bool, v_keyname string, v_vararr []orderedValue) func(tags element.Tags, key Key, closed bool) bool {

	if findValueInOrderedValue("__nil__", v_vararr) { // check __nil__
		log.Print("warn: Filter value '__nil__' is not supported ! (tablename:" + tablename + ")")
	}

	if findValueInOrderedValue("__any__", v_vararr) { // check __any__
		if len(v_vararr) > 1 {
			log.Print("warn: Multiple filter value with '__any__' keywords is not valid! (tablename:" + tablename + ")")
		}
		return func(tags element.Tags, key Key, closed bool) bool {
			if _, ok := tags[v_keyname]; ok {
				return virtualTrue
			}
			return virtualFalse
		}
	} else if len(v_vararr) == 1 { //  IF 1 parameter  THEN we can generate optimal code
		return func(tags element.Tags, key Key, closed bool) bool {
			if v, ok := tags[v_keyname]; ok {
				if Value(v) == v_vararr[0].value {
					return virtualTrue
				}
			}
			return virtualFalse
		}
	} else { //  > 1 parameter  - less optimal code
		return func(tags element.Tags, key Key, closed bool) bool {
			if v, ok := tags[v_keyname]; ok {
				if findValueInOrderedValue(Value(v), v_vararr) {
					return virtualTrue
				}
			}
			return virtualFalse
		}
	}
}
