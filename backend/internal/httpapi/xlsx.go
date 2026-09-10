package httpapi

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
)

func writeXLSX(dst io.Writer, rows [][]string) error {
	z := zip.NewWriter(dst)
	files := map[string]string{
		"[Content_Types].xml":        `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
		"_rels/.rels":                `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":            `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Аналитика" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
	}
	for name, body := range files {
		w, err := z.Create(name)
		if err != nil {
			return err
		}
		if _, err = io.WriteString(w, body); err != nil {
			return err
		}
	}
	sheet, err := z.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		return err
	}
	if _, err = io.WriteString(sheet, `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`); err != nil {
		return err
	}
	for r, row := range rows {
		if _, err = fmt.Fprintf(sheet, `<row r="%d">`, r+1); err != nil {
			return err
		}
		for c, value := range row {
			cell := columnName(c+1) + fmt.Sprint(r+1)
			if _, err = fmt.Fprintf(sheet, `<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, cell, xmlText(value)); err != nil {
				return err
			}
		}
		if _, err = io.WriteString(sheet, `</row>`); err != nil {
			return err
		}
	}
	if _, err = io.WriteString(sheet, `</sheetData></worksheet>`); err != nil {
		return err
	}
	return z.Close()
}

func columnName(n int) string {
	var value string
	for n > 0 {
		n--
		value = string(rune('A'+n%26)) + value
		n /= 26
	}
	return value
}

func xmlText(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}
