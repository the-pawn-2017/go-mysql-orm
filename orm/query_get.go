package orm

import (
    "database/sql"
    "errors"
    "reflect"
    "strings"
)

func (m Query[T]) Get() (T, QueryResult) {
    ret := new(T)
    res := m.Limit(1).GetTo(ret)
    return *ret, res
}

func (m Query[T]) GetList() ([]T, QueryResult) {
    ret := make([]T, 0)
    res := m.GetTo(&ret)
    return ret, res
}

func (m Query[T]) GetTo(destPtr interface{}) QueryResult {
    tempTable := m.generateSelectQuery(m.columns...)

    m.result.PrepareSql = tempTable.raw
    m.result.Bindings = tempTable.bindings

    if m.result.Err != nil {
        return m.result
    }

    var rows *sql.Rows
    var err error
    if m.dbTx() != nil {
        rows, err = m.dbTx().Query(tempTable.raw, tempTable.bindings...)
    } else {
        rows, err = m.DB().Query(tempTable.raw, tempTable.bindings...)
    }

    defer func() {
        if rows != nil {
            _ = rows.Close()
        }
    }()

    if err != nil {
        m.result.Err = err
        return m.result
    }

    m.result.Err = m.scanRows(destPtr, rows)
    return m.result
}

func (m Query[T]) generateSelectColumns(columns ...interface{}) string {
    var outColumns []string
    for _, v := range columns {
        column, err := m.parseColumn(v)
        if err != nil {
            m.result.Err = err
            return ""
        }
        outColumns = append(outColumns, column) //column string name
    }

    if len(outColumns) == 0 {
        return "*"
    } else {
        return strings.Join(outColumns, ",")
    }
}

func (m Query[T]) generateSelectQuery(columns ...interface{}) tempTable {
    bindings := make([]interface{}, 0)

    selectStr := m.generateSelectColumns(columns...)

    tableStr := m.generateTableAndJoinStr(m.tables, &bindings)

    whereStr := m.generateWhereStr(m.wheres, &bindings)

    orderLimitOffsetStr := m.getOrderAndLimitSqlStr()

    rawSql := "select " + selectStr
    if m.forUpdate {
        rawSql += " for update"
    }
    if tableStr != "" {
        rawSql += " from " + tableStr
        if whereStr != "" {
            rawSql += " where " + whereStr
        }
    }

    if orderLimitOffsetStr != "" {
        rawSql += " " + orderLimitOffsetStr
    }

    var ret tempTable
    ret.raw = rawSql
    ret.bindings = bindings
    return ret
}

func (m Query[T]) scanValues(baseAddrs []interface{}, rowColumns []string, rows *sql.Rows, setVal func(), tryOnce bool) error {
    var err error
    var tempAddrs = make([]interface{}, len(rowColumns))
    for k := range rowColumns {
        var temp interface{}
        tempAddrs[k] = &temp
    }

    finalAddrs := make([]interface{}, len(rowColumns))

    for rows.Next() {
        err = rows.Scan(tempAddrs...)
        if err != nil {
            return err
        }

        for k, v := range tempAddrs {
            if reflect.ValueOf(v).Elem().IsNil() {
                felement := reflect.ValueOf(baseAddrs[k]).Elem()
                felement.Set(reflect.Zero(felement.Type()))
                finalAddrs[k] = v
            } else {
                finalAddrs[k] = baseAddrs[k]
            }
        }

        err = rows.Scan(finalAddrs...)
        if setVal != nil {
            setVal()
        }
        if tryOnce {
            break
        }
    }
    if err == nil {
        err = rows.Err()
    }
    return err
}

func (m Query[T]) scanRows(dest interface{}, rows *sql.Rows) error {
    rowColumns, err := rows.Columns()
    if err != nil {
        return err
    }
    base := reflect.ValueOf(dest)
    if base.Kind() != reflect.Ptr {
        return errors.New("select dest must be ptr")
    }
    val := base.Elem()
    if val.Kind() == reflect.Ptr {
        return errors.New("select dest must be ptr")
    }

    switch val.Kind() {
    case reflect.Map:
        ele := reflect.TypeOf(dest).Elem().Elem()
        if ele.Kind() == reflect.Ptr {
            return errors.New("select dest slice element must not be ptr")
        }

        newVal := reflect.MakeMap(reflect.TypeOf(dest).Elem())
        switch ele.Kind() {
        case reflect.Struct:
            structAddr := reflect.New(ele).Interface()
            structAddrMap, err := getStructFieldAddrMap(structAddr)
            if err != nil {
                return err
            }
            var baseAddrs = make([]interface{}, len(rowColumns))

            for k, v := range rowColumns {
                baseAddrs[k] = structAddrMap[v]
                if baseAddrs[k] == nil {
                    var temp interface{}
                    baseAddrs[k] = &temp
                }
            }
            err = m.scanValues(baseAddrs, rowColumns, rows, func() {
                newVal.SetMapIndex(reflect.ValueOf(baseAddrs[0]).Elem(), reflect.ValueOf(structAddr).Elem())
            }, false)
            base.Elem().Set(newVal)
        case reflect.Slice:
            if ele.Elem().Kind() != reflect.Struct {
                return errors.New("map slice only struct item allowed")
            }
            structAddr := reflect.New(ele.Elem()).Interface()
            structAddrMap, err := getStructFieldAddrMap(structAddr)
            if err != nil {
                return err
            }
            var baseAddrs = make([]interface{}, len(rowColumns))

            for k, v := range rowColumns {
                baseAddrs[k] = structAddrMap[v]
                if baseAddrs[k] == nil {
                    var temp interface{}
                    baseAddrs[k] = &temp
                }
            }
            err = m.scanValues(baseAddrs, rowColumns, rows, func() {
                index := reflect.ValueOf(baseAddrs[0]).Elem()
                tempSlice := newVal.MapIndex(index)
                if tempSlice.IsValid() == false {
                    tempSlice = reflect.MakeSlice(ele, 0, 0)
                }
                newVal.SetMapIndex(index, reflect.Append(tempSlice, reflect.ValueOf(structAddr).Elem()))
            }, false)
            base.Elem().Set(newVal)

        case reflect.Interface:
            if reflect.TypeOf(dest).Elem().Key().Kind() == reflect.String {

                var baseAddrs = make([]interface{}, len(rowColumns))
                for k := range baseAddrs {
                    var temp interface{}
                    baseAddrs[k] = &temp
                }

                err = m.scanValues(baseAddrs, rowColumns, rows, func() {
                    for k, v := range rowColumns {
                        newVal.SetMapIndex(reflect.ValueOf(v), reflect.ValueOf(baseAddrs[k]).Elem())
                    }
                }, true)

                base.Elem().Set(newVal)
                return err
            }
            fallthrough
        default:
            keyType := reflect.TypeOf(dest).Elem().Key()

            keyAddr := reflect.New(keyType).Interface()
            tempAddr := reflect.New(ele).Interface()

            var baseAddrs = make([]interface{}, len(rowColumns))

            for k := 0; k < len(rowColumns); k++ {
                if k == 0 {
                    baseAddrs[k] = keyAddr
                } else if k == 1 {
                    baseAddrs[k] = tempAddr
                } else {
                    var temp interface{}
                    baseAddrs[k] = &temp
                }
            }
            err = m.scanValues(baseAddrs, rowColumns, rows, func() {
                newVal.SetMapIndex(reflect.ValueOf(keyAddr).Elem(), reflect.ValueOf(tempAddr).Elem())
            }, false)

            base.Elem().Set(newVal)
        }
    case reflect.Struct:
        structAddr := dest
        structAddrMap, err := getStructFieldAddrMap(structAddr)
        if err != nil {
            return err
        }
        var baseAddrs = make([]interface{}, len(rowColumns))

        for k, v := range rowColumns {
            baseAddrs[k] = structAddrMap[v]
            if baseAddrs[k] == nil {
                var temp interface{}
                baseAddrs[k] = &temp
            }
        }
        err = m.scanValues(baseAddrs, rowColumns, rows, nil, true)
    case reflect.Slice:
        ele := reflect.TypeOf(dest).Elem().Elem()
        if ele.Kind() == reflect.Ptr {
            return errors.New("select dest slice element must not be ptr")
        }

        switch ele.Kind() {
        case reflect.Struct:
            structAddr := reflect.New(ele).Interface()
            structAddrMap, err := getStructFieldAddrMap(structAddr)
            if err != nil {
                return err
            }
            var baseAddrs = make([]interface{}, len(rowColumns))

            for k, v := range rowColumns {
                baseAddrs[k] = structAddrMap[v]
                if baseAddrs[k] == nil {
                    var temp interface{}
                    baseAddrs[k] = &temp
                }
            }

            err = m.scanValues(baseAddrs, rowColumns, rows, func() {
                val = reflect.Append(val, reflect.ValueOf(structAddr).Elem())
            }, false)

            base.Elem().Set(val)
        case reflect.Map:
            var baseAddrs = make([]interface{}, len(rowColumns))

            for k := range baseAddrs {
                var temp interface{}
                baseAddrs[k] = &temp
            }
            err = m.scanValues(baseAddrs, rowColumns, rows, func() {
                newVal := reflect.MakeMap(ele)
                for k, v := range rowColumns {
                    newVal.SetMapIndex(reflect.ValueOf(v), reflect.ValueOf(baseAddrs[k]).Elem())
                }
                val = reflect.Append(val, newVal)
            }, false)

            base.Elem().Set(val)
        default:
            tempAddr := reflect.New(ele).Interface()

            var baseAddrs = make([]interface{}, len(rowColumns))

            for k := 0; k < len(rowColumns); k++ {
                if k == 0 {
                    baseAddrs[k] = tempAddr
                } else {
                    var temp interface{}
                    baseAddrs[k] = &temp
                }
            }

            err = m.scanValues(baseAddrs, rowColumns, rows, func() {
                val = reflect.Append(val, reflect.ValueOf(tempAddr).Elem())
            }, false)

            base.Elem().Set(val)
        }
    default:
        var baseAddrs = make([]interface{}, len(rowColumns))
        for k := 0; k < len(rowColumns); k++ {
            if k == 0 {
                baseAddrs[k] = dest
            } else {
                var temp interface{}
                baseAddrs[k] = &temp
            }
        }
        err = m.scanValues(baseAddrs, rowColumns, rows, nil, true)
    }
    return err
}