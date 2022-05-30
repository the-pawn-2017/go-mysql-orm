package orm

import (
    "errors"
    "fmt"
    "github.com/gobeam/stringy"
    "io/ioutil"
    "reflect"
    "regexp"
    "strings"
    "time"
)

var findCommentRegex = regexp.MustCompile("(.+) COMMENT '(.+)'")
var findDefaultRegex = regexp.MustCompile("(.+) DEFAULT (.+)")
var findAutoIncrementRegex = regexp.MustCompile("(.+) AUTO_INCREMENT")
var findNotNullRegex = regexp.MustCompile("(.+) NOT NULL")
var findNullRegex = regexp.MustCompile("(.+) NULL")

func CreateStructFromTable(table Table) error {

    dbColumns, err := getTableDbColumns(table)
    if err != nil {
        return err
    }

    var structLines []string
    for _, v := range dbColumns {
        structFieldName := stringy.New(v.Name).CamelCase()
        sturctFieldType := getStructFieldTypeStringByDBType(v.Type)
        if v.Null {
            sturctFieldType = "*" + sturctFieldType
        }

        var structFieldTags []string
        structFieldTags = append(structFieldTags, fmt.Sprintf("json:\"%s\"", v.Name))
        var ormTags []string
        ormTags = append(ormTags, v.Name)
        ormTags = append(ormTags, v.Type)
        if v.Null {
            ormTags = append(ormTags, nullPrefix)
        }
        if v.AutoIncrement {
            ormTags = append(ormTags, autoIncrementPrefix)
        }
        if v.Primary {
            ormTags = append(ormTags, primaryKeyPrefix)
        }
        if v.Unique {
            ormTags = append(ormTags, uniqueKeyPrefix)
        }
        if v.Index {
            ormTags = append(ormTags, keyPrefix)
        }
        if len(v.Uniques) > 0 {
            ormTags = append(ormTags, v.Uniques...)
        }
        if len(v.Indexs) > 0 {
            ormTags = append(ormTags, v.Indexs...)
        }

        structFieldTags = append(structFieldTags, fmt.Sprintf("orm:\"%s\"", strings.Join(ormTags, ",")))

        if v.Default != "" {
            structFieldTags = append(structFieldTags, fmt.Sprintf("default:\"%s\"", v.Default))
        }
        if v.Comment != "" {
            structFieldTags = append(structFieldTags, fmt.Sprintf("comment:\"%s\"", v.Comment))
        }

        line := structFieldName + " " + sturctFieldType + " " + "`" + strings.Join(structFieldTags, " ") + "`"

        structLines = append(structLines, line)
    }

    structFile := "" //table.Query().currentFilename()

    fileBytes, err := ioutil.ReadFile(structFile)
    if err != nil {
        return err
    }

    fileContent := string(fileBytes)

    structNameSrc := strings.Split(reflect.TypeOf(table).Elem().String(), ".")
    structName := structNameSrc[len(structNameSrc)-1]

    search := "type " + structName + " struct {"
    oldStructRename := "type " + structName + "_" + time.Now().Format("2006_01_02_15_04_05") + " struct {"

    fileParts := strings.SplitN(fileContent, search, 2)

    finalFileContent := fileParts[0] + search + "\n" + strings.Join(structLines, "\n") + "\n}\n" + oldStructRename + fileParts[1]

    return ioutil.WriteFile(structFile, []byte(finalFileContent), 0644)
}

func getStructFieldTypeStringByDBType(dbType string) string {
    if strings.Contains(dbType, "char") || strings.Contains(dbType, "text") {
        return "string"
    }
    if strings.Contains(dbType, "int") {
        if strings.Contains(dbType, "unsigned") {
            if strings.HasPrefix(dbType, "tiny") {
                return "uint8"
            } else if strings.HasPrefix(dbType, "big") {
                return "uint64"
            } else {
                return "uint"
            }
        } else {
            if strings.HasPrefix(dbType, "tiny") {
                return "int8"
            } else if strings.HasPrefix(dbType, "big") {
                return "int64"
            } else {
                return "int"
            }
        }
    } else if strings.Contains(dbType, "float") || strings.Contains(dbType, "double") || strings.Contains(dbType, "decimal") {
        return "float64"
    } else if strings.Contains(dbType, "time") || strings.Contains(dbType, "date") {
        return "time.Time"
    }
    return "string"
}

func getSqlSegments(table Table) ([]string, error) {
    var res map[string]string
    //err := table.Query().SelectRaw(&res, "show create table "+table.TableName()).Err
    var err error
    if err != nil {
        return nil, err
    }

    createTableSql := res[table.TableName()]
    if createTableSql == "" {
        return nil, errors.New("create table sql not exist")
    }

    sqlSegments := strings.Split(createTableSql, "\n")

    if len(sqlSegments) <= 2 {
        return nil, errors.New(createTableSql)
    }
    sqlSegments = sqlSegments[1 : len(sqlSegments)-1]
    return sqlSegments, nil
}

func getTableDbColumns(table Table) ([]dBColumn, error) {
    sqlSegments, err := getSqlSegments(table)
    if err != nil {
        return nil, err
    }

    ret := make([]dBColumn, 0)
    existColumn := make(map[string]int)

    for k, v := range sqlSegments {
        v = strings.TrimLeft(v, " ")
        v = strings.TrimRight(v, ",")

        if strings.HasPrefix(v, "PRIMARY KEY ") {
            v = strings.TrimPrefix(v, "PRIMARY KEY ")
            keyNameAndCols := strings.Trim(v, "()")
            keyNameAndCols = strings.Trim(v, "`")
            ret[existColumn[keyNameAndCols]].Primary = true
        } else if strings.HasPrefix(v, "UNIQUE KEY ") {
            v = strings.TrimPrefix(v, "UNIQUE KEY ")
            keyNameAndCols := strings.Split(v, " ")
            if len(keyNameAndCols) != 2 {
                continue
            }

            keyName := strings.Trim(keyNameAndCols[0], "`")
            cols := strings.Split(strings.Trim(keyNameAndCols[1], "()"), ",")

            if len(cols) == 1 {
                colName := strings.Trim(cols[0], "`")
                ret[existColumn[colName]].Unique = true
            } else {
                if strings.HasPrefix(keyName, "unique_") == false {
                    keyName = "unique_" + keyName
                }
                for _, v2 := range cols {
                    colName := strings.Trim(v2, "`")

                    ret[existColumn[colName]].Uniques = append(ret[existColumn[colName]].Uniques, keyName)
                }
            }
        } else if strings.HasPrefix(v, "KEY ") {
            v = strings.TrimPrefix(v, "KEY ")
            keyNameAndCols := strings.Split(v, " ")
            if len(keyNameAndCols) != 2 {
                continue
            }

            keyName := strings.Trim(keyNameAndCols[0], "`")
            cols := strings.Split(strings.Trim(keyNameAndCols[1], "()"), ",")

            if len(cols) == 1 {
                colName := strings.Trim(cols[0], "`")
                ret[existColumn[colName]].Index = true
            } else {
                if strings.HasPrefix(keyName, "index_") == false {
                    keyName = "index_" + keyName
                }
                for _, v2 := range cols {
                    colName := strings.Trim(v2, "`")

                    ret[existColumn[colName]].Indexs = append(ret[existColumn[colName]].Indexs, keyName)
                }
            }
        } else {
            var col dBColumn
            col.Null = true
            temp := findCommentRegex.FindStringSubmatch(v)
            if len(temp) >= 3 {
                v = temp[1]
                col.Comment = temp[2]
            }

            temp = findDefaultRegex.FindStringSubmatch(v)
            if len(temp) >= 3 {
                v = temp[1]
                col.Default = strings.Trim(temp[2], "'")
            }

            temp = findAutoIncrementRegex.FindStringSubmatch(v)
            if len(temp) >= 2 {
                v = temp[1]
                col.AutoIncrement = true
            }

            temp = findNotNullRegex.FindStringSubmatch(v)
            if len(temp) >= 2 {
                v = temp[1]
                col.Null = false
            }

            temp = findNullRegex.FindStringSubmatch(v)
            if len(temp) >= 2 {
                v = temp[1]
            }

            nameAndTypeStrs := strings.SplitN(v, " ", 2)
            if len(nameAndTypeStrs) != 2 {
                continue
            }

            col.Type = nameAndTypeStrs[1]
            col.Name = strings.Trim(nameAndTypeStrs[0], "`")
            existColumn[col.Name] = k
            ret = append(ret, col)
        }
    }

    return ret, nil
}
