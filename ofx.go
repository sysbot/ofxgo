package ofxgo

import (
	"bufio"
	"bytes"
	"errors"
	"github.com/golang/go/src/encoding/xml"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	// Request fields to overwrite with the client's values. If nonempty,
	// defaults are used
	SpecVersion string // VERSION in header
	AppId       string // SONRQ>APPID
	AppVer      string // SONRQ>APPVER

	// Don't insert newlines or indentation when marshalling to SGML/XML
	NoIndent bool
}

var defaultClient Client

func (c *Client) OfxVersion() string {
	if len(c.SpecVersion) > 0 {
		return c.SpecVersion
	} else {
		return "203"
	}
}

func (c *Client) Id() String {
	if len(c.AppId) > 0 {
		return String(c.AppId)
	} else {
		return String("OFXGO")
	}
}

func (c *Client) Version() String {
	if len(c.AppVer) > 0 {
		return String(c.AppVer)
	} else {
		return String("0001")
	}
}

func (c *Client) IndentRequests() bool {
	return !c.NoIndent
}

func RawRequest(URL string, r io.Reader) (*http.Response, error) {
	response, err := http.Post(URL, "application/x-ofx", r)
	if err != nil {
		return nil, err
	}

	if response.StatusCode != 200 {
		return nil, errors.New("OFXQuery request status: " + response.Status)
	}

	return response, nil
}

// Request marshals a Request object into XML, makes an HTTP request against
// it's URL, and then unmarshals the response into a Reaponse object.
//
// Before being marshaled, some of the the Request object's values are
// overwritten, namely those dictated by the Client's configuration (Version,
// AppId, AppVer fields), and the client's curren time (DtClient). These are
// updated in place in the supplied Request object so they may later be
// inspected by the caller.
func (c *Client) Request(r *Request) (*Response, error) {
	r.Signon.DtClient = Date(time.Now())

	// Overwrite fields that the client controls
	r.Version = c.OfxVersion()
	r.Signon.AppId = c.Id()
	r.Signon.AppVer = c.Version()
	r.indent = c.IndentRequests()

	b, err := r.Marshal()
	if err != nil {
		return nil, err
	}

	response, err := RawRequest(r.URL, b)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	ofxresp, err := ParseResponse(response.Body)
	if err != nil {
		return nil, err
	}
	return ofxresp, nil
}

type Message interface {
	Name() string
	Valid() (bool, error)
}

type Request struct {
	URL     string
	Version string        // String for OFX header, defaults to 203
	Signon  SignonRequest //<SIGNONMSGSETV1>
	Signup  []Message     //<SIGNUPMSGSETV1>
	Banking []Message     //<BANKMSGSETV1>
	//<CREDITCARDMSGSETV1>
	//<LOANMSGSETV1>
	//<INVSTMTMSGSETV1>
	//<INTERXFERMSGSETV1>
	//<WIREXFERMSGSETV1>
	//<BILLPAYMSGSETV1>
	//<EMAILMSGSETV1>
	//<SECLISTMSGSETV1>
	//<PRESDIRMSGSETV1>
	//<PRESDLVMSGSETV1>
	Profile []Message //<PROFMSGSETV1>
	//<IMAGEMSGSETV1>

	indent bool // Whether to indent the marshaled XML
}

func (oq *Request) marshalMessageSet(e *xml.Encoder, requests []Message, setname string) error {
	if len(requests) > 0 {
		messageSetElement := xml.StartElement{Name: xml.Name{Local: setname}}
		if err := e.EncodeToken(messageSetElement); err != nil {
			return err
		}

		for _, request := range requests {
			if ok, err := request.Valid(); !ok {
				return err
			}
			if err := e.Encode(request); err != nil {
				return err
			}
		}

		if err := e.EncodeToken(messageSetElement.End()); err != nil {
			return err
		}
	}
	return nil
}

func (oq *Request) Marshal() (*bytes.Buffer, error) {
	var b bytes.Buffer

	// Write the header appropriate to our version
	switch oq.Version {
	case "102", "103", "151", "160":
		b.WriteString(`OFXHEADER:100
DATA:OFXSGML
VERSION:` + oq.Version + `
SECURITY:NONE
ENCODING:USASCII
CHARSET:1252
COMPRESSION:NONE
OLDFILEUID:NONE
NEWFILEUID:NONE

`)
	case "200", "201", "202", "203", "210", "211", "220":
		b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="no"?>` + "\n")
		b.WriteString(`<?OFX OFXHEADER="200" VERSION="` + oq.Version + `" SECURITY="NONE" OLDFILEUID="NONE" NEWFILEUID="NONE"?>` + "\n")
	default:
		return nil, errors.New(oq.Version + " is not a valid OFX version string")
	}

	encoder := xml.NewEncoder(&b)
	if oq.indent {
		encoder.Indent("", "    ")
	}

	ofxElement := xml.StartElement{Name: xml.Name{Local: "OFX"}}

	if err := encoder.EncodeToken(ofxElement); err != nil {
		return nil, err
	}

	if ok, err := oq.Signon.Valid(); !ok {
		return nil, err
	}
	signonMsgSet := xml.StartElement{Name: xml.Name{Local: "SIGNONMSGSRQV1"}}
	if err := encoder.EncodeToken(signonMsgSet); err != nil {
		return nil, err
	}
	if err := encoder.Encode(&oq.Signon); err != nil {
		return nil, err
	}
	if err := encoder.EncodeToken(signonMsgSet.End()); err != nil {
		return nil, err
	}

	if err := oq.marshalMessageSet(encoder, oq.Signup, "SIGNUPMSGSRQV1"); err != nil {
		return nil, err
	}
	if err := oq.marshalMessageSet(encoder, oq.Banking, "BANKMSGSRQV1"); err != nil {
		return nil, err
	}
	if err := oq.marshalMessageSet(encoder, oq.Profile, "PROFMSGSRQV1"); err != nil {
		return nil, err
	}

	if err := encoder.EncodeToken(ofxElement.End()); err != nil {
		return nil, err
	}

	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	return &b, nil
}

type Response struct {
	Version string         // String for OFX header, defaults to 203
	Signon  SignonResponse //<SIGNONMSGSETV1>
	Signup  []Message      //<SIGNUPMSGSETV1>
	Banking []Message      //<BANKMSGSETV1>
	//<CREDITCARDMSGSETV1>
	//<LOANMSGSETV1>
	//<INVSTMTMSGSETV1>
	//<INTERXFERMSGSETV1>
	//<WIREXFERMSGSETV1>
	//<BILLPAYMSGSETV1>
	//<EMAILMSGSETV1>
	//<SECLISTMSGSETV1>
	//<PRESDIRMSGSETV1>
	//<PRESDLVMSGSETV1>
	Profile []Message //<PROFMSGSETV1>
	//<IMAGEMSGSETV1>
}

func (or *Response) readSGMLHeaders(r *bufio.Reader) error {
	var seenHeader, seenVersion bool = false, false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		// r.ReadString leaves the '\n' on the end...
		line = strings.TrimSpace(line)

		if len(line) == 0 {
			if seenHeader {
				break
			} else {
				continue
			}
		}
		header := strings.SplitN(line, ":", 2)
		if header == nil || len(header) != 2 {
			return errors.New("OFX headers malformed")
		}

		switch header[0] {
		case "OFXHEADER":
			if header[1] != "100" {
				return errors.New("OFXHEADER is not 100")
			}
			seenHeader = true
		case "DATA":
			if header[1] != "OFXSGML" {
				return errors.New("OFX DATA header does not contain OFXSGML")
			}
		case "VERSION":
			switch header[1] {
			case "102", "103", "151", "160":
				seenVersion = true
				or.Version = header[1]
			default:
				return errors.New("Invalid OFX VERSION in header")
			}
		case "SECURITY":
			if header[1] != "NONE" {
				return errors.New("OFX SECURITY header not NONE")
			}
		case "COMPRESSION":
			if header[1] != "NONE" {
				return errors.New("OFX COMPRESSION header not NONE")
			}
		case "ENCODING", "CHARSET", "OLDFILEUID", "NEWFILEUID":
			// TODO check/handle these headers?
		default:
			return errors.New("Invalid OFX header: " + header[0])
		}
	}

	if !seenVersion {
		return errors.New("OFX VERSION header missing")
	}
	return nil
}

func nextNonWhitespaceToken(decoder *xml.Decoder) (xml.Token, error) {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		} else if chars, ok := tok.(xml.CharData); ok {
			strippedBytes := bytes.TrimSpace(chars)
			if len(strippedBytes) != 0 {
				return tok, nil
			}
		} else {
			return tok, nil
		}
	}
}

func (or *Response) readXMLHeaders(decoder *xml.Decoder) error {
	var tok xml.Token
	tok, err := nextNonWhitespaceToken(decoder)
	if err != nil {
		return err
	} else if xmlElem, ok := tok.(xml.ProcInst); !ok || xmlElem.Target != "xml" {
		return errors.New("Missing xml processing instruction")
	}

	// parse the OFX header
	tok, err = nextNonWhitespaceToken(decoder)
	if err != nil {
		return err
	} else if ofxElem, ok := tok.(xml.ProcInst); ok && ofxElem.Target == "OFX" {
		var seenHeader, seenVersion bool = false, false

		headers := bytes.TrimSpace(ofxElem.Inst)
		for len(headers) > 0 {
			tmp := bytes.SplitN(headers, []byte("=\""), 2)
			if len(tmp) != 2 {
				return errors.New("Malformed OFX header")
			}
			header := string(tmp[0])
			headers = tmp[1]
			tmp = bytes.SplitN(headers, []byte("\""), 2)
			if len(tmp) != 2 {
				return errors.New("Malformed OFX header")
			}
			value := string(tmp[0])
			headers = bytes.TrimSpace(tmp[1])

			switch header {
			case "OFXHEADER":
				if value != "200" {
					return errors.New("OFXHEADER is not 200")
				}
				seenHeader = true
			case "VERSION":
				switch value {
				case "200", "201", "202", "203", "210", "211", "220":
					seenVersion = true
					or.Version = value
				default:
					return errors.New("Invalid OFX VERSION in header")
				}
			case "SECURITY":
				if value != "NONE" {
					return errors.New("OFX SECURITY header not NONE")
				}
			case "OLDFILEUID", "NEWFILEUID":
				// TODO check/handle these headers?
			default:
				return errors.New("Invalid OFX header: " + header)
			}
		}

		if !seenHeader {
			return errors.New("OFXHEADER version missing")
		}
		if !seenVersion {
			return errors.New("OFX VERSION header missing")
		}

	} else {
		return errors.New("Missing xml 'OFX' processing instruction")
	}
	return nil
}

const guessVersionCheckBytes = 1024

// Defaults to XML if it can't determine the version based on the first 1024
// bytes, or if there is any ambiguity
func guessVersion(r *bufio.Reader) (bool, error) {
	b, _ := r.Peek(guessVersionCheckBytes)
	if b == nil {
		return false, errors.New("Failed to read OFX header")
	}
	sgmlIndex := bytes.Index(b, []byte("OFXHEADER:"))
	xmlIndex := bytes.Index(b, []byte("OFXHEADER="))
	if sgmlIndex < 0 {
		return true, nil
	} else if xmlIndex < 0 {
		return false, nil
	} else {
		return xmlIndex <= sgmlIndex, nil
	}
}

// ParseResponse parses an OFX response in SGML or XML into a Response object
// from the given io.Reader
//
// It is commonly used as part of Client.Request(), but may be used on its own
// to parse already-downloaded OFX files (such as those from 'Web Connect'). It
// performs version autodetection if it can and attempts to be as forgiving as
// possible about the input format.
func ParseResponse(reader io.Reader) (*Response, error) {
	var or Response

	r := bufio.NewReaderSize(reader, guessVersionCheckBytes)
	xmlVersion, err := guessVersion(r)
	if err != nil {
		return nil, err
	}

	// parse SGML headers before creating XML decoder
	if !xmlVersion {
		if err := or.readSGMLHeaders(r); err != nil {
			return nil, err
		}
	}

	decoder := xml.NewDecoder(r)
	if !xmlVersion {
		decoder.Strict = false
		decoder.AutoCloseAfterCharData = ofxLeafElements
	}
	decoder.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return input, nil
	}

	if xmlVersion {
		// parse the xml header
		if err := or.readXMLHeaders(decoder); err != nil {
			return nil, err
		}
	}

	tok, err := nextNonWhitespaceToken(decoder)
	if err != nil {
		return nil, err
	} else if ofxStart, ok := tok.(xml.StartElement); !ok || ofxStart.Name.Local != "OFX" {
		return nil, errors.New("Missing opening OFX xml element")
	}

	// Unmarshal the signon message
	tok, err = nextNonWhitespaceToken(decoder)
	if err != nil {
		return nil, err
	} else if signonStart, ok := tok.(xml.StartElement); ok && signonStart.Name.Local == "SIGNONMSGSRSV1" {
		if err := decoder.Decode(&or.Signon); err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("Missing opening SIGNONMSGSRSV1 xml element")
	}

	tok, err = nextNonWhitespaceToken(decoder)
	if err != nil {
		return nil, err
	} else if signonEnd, ok := tok.(xml.EndElement); !ok || signonEnd.Name.Local != "SIGNONMSGSRSV1" {
		return nil, errors.New("Missing closing SIGNONMSGSRSV1 xml element")
	}
	if ok, err := or.Signon.Valid(); !ok {
		return nil, err
	}

	for {
		tok, err = nextNonWhitespaceToken(decoder)
		if err != nil {
			return nil, err
		} else if ofxEnd, ok := tok.(xml.EndElement); ok && ofxEnd.Name.Local == "OFX" {
			return &or, nil // found closing XML element, so we're done
		} else if start, ok := tok.(xml.StartElement); ok {
			// TODO decode other types
			switch start.Name.Local {
			case "SIGNUPMSGSRSV1":
				msgs, err := DecodeSignupMessageSet(decoder, start)
				if err != nil {
					return nil, err
				}
				or.Signup = msgs
			case "BANKMSGSRSV1":
				msgs, err := DecodeBankingMessageSet(decoder, start)
				if err != nil {
					return nil, err
				}
				or.Banking = msgs
			//case "CREDITCARDMSGSRSV1":
			//case "LOANMSGSRSV1":
			//case "INVSTMTMSGSRSV1":
			//case "INTERXFERMSGSRSV1":
			//case "WIREXFERMSGSRSV1":
			//case "BILLPAYMSGSRSV1":
			//case "EMAILMSGSRSV1":
			//case "SECLISTMSGSRSV1":
			//case "PRESDIRMSGSRSV1":
			//case "PRESDLVMSGSRSV1":
			case "PROFMSGSRSV1":
				msgs, err := DecodeProfileMessageSet(decoder, start)
				if err != nil {
					return nil, err
				}
				or.Profile = msgs
			//case "IMAGEMSGSRSV1":
			default:
				return nil, errors.New("Unsupported message set: " + start.Name.Local)
			}
		} else {
			return nil, errors.New("Found unexpected token")
		}
	}
}