package clickup

import (
	"bytes"
	"encoding/json"
)

// ClickUp uses string identifiers, but a few endpoints have historically
// returned numeric identifiers. Keeping that detail in the transport layer
// prevents it from reaching the domain model.
type wireString string

func (s *wireString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) || len(data) == 0 {
		*s = ""
		return nil
	}

	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*s = wireString(value)
		return nil
	}

	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return err
	}
	*s = wireString(number.String())
	return nil
}

func (s wireString) String() string {
	return string(s)
}

type teamsResponse struct {
	Teams []wireTeam `json:"teams"`
}

type wireTeam struct {
	ID   wireString `json:"id"`
	Name string     `json:"name"`
}

type spacesResponse struct {
	Spaces []wireSpace `json:"spaces"`
}

type wireSpace struct {
	ID       wireString `json:"id"`
	Name     string     `json:"name"`
	TeamID   string     `json:"-"`
	TeamName string     `json:"-"`
}

type foldersResponse struct {
	Folders []wireFolder `json:"folders"`
}

type wireFolder struct {
	ID wireString `json:"id"`
}

type listsResponse struct {
	Lists []wireList `json:"lists"`
}

type wireList struct {
	ID   wireString `json:"id"`
	Name string     `json:"name"`
}

type tasksResponse struct {
	Tasks    []wireTask `json:"tasks"`
	LastPage bool       `json:"last_page"`
}

type wireTask struct {
	ID          wireString     `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Status      wireStatus     `json:"status"`
	Priority    *wirePriority  `json:"priority"`
	DueDate     *wireString    `json:"due_date"`
	DateCreated wireString     `json:"date_created"`
	DateUpdated wireString     `json:"date_updated"`
	DateClosed  *wireString    `json:"date_closed"`
	DateDone    *wireString    `json:"date_done"`
	Parent      *wireString    `json:"parent"`
	Assignees   []wireAssignee `json:"assignees"`
	List        wireList       `json:"list"`
	Lists       []wireList     `json:"lists"`
}

type wireAssignee struct {
	ID       wireString `json:"id"`
	Username string     `json:"username"`
	Name     string     `json:"name"`
}

type wireStatus struct {
	Status string `json:"status"`
	Color  string `json:"color"`
	Type   string `json:"type"`
}

type wirePriority struct {
	Priority   string     `json:"priority"`
	Color      string     `json:"color"`
	OrderIndex wireString `json:"orderindex"`
}

type taskCreatePayload struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Status      string  `json:"status,omitempty"`
	DueDate     *int64  `json:"due_date,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
	Parent      *string `json:"parent,omitempty"`
}

type taskUpdatePayload struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	DueDate     *int64  `json:"due_date"`
	Priority    *int    `json:"priority,omitempty"`
	Parent      *string `json:"parent,omitempty"`
}
