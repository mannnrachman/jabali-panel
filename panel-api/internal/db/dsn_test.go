package db_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/db"
)

func TestToDriverDSN(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "URL form full",
			in:   "mysql://jabali:secret@127.0.0.1:3306/jabali_panel?parseTime=true&charset=utf8mb4&loc=UTC",
			want: "jabali:secret@tcp(127.0.0.1:3306)/jabali_panel?parseTime=true&charset=utf8mb4&loc=UTC",
		},
		{
			name: "URL form no query",
			in:   "mysql://u:p@db:3306/x",
			want: "u:p@tcp(db:3306)/x",
		},
		{
			name: "URL form mariadb scheme",
			in:   "mariadb://u:p@db:3306/x?parseTime=true",
			want: "u:p@tcp(db:3306)/x?parseTime=true",
		},
		{
			name: "native driver form passes through",
			in:   "u:p@tcp(db:3306)/x?parseTime=true",
			want: "u:p@tcp(db:3306)/x?parseTime=true",
		},
		{
			name: "unix socket form passes through",
			in:   "u:p@unix(/run/mysqld/mysqld.sock)/x",
			want: "u:p@unix(/run/mysqld/mysqld.sock)/x",
		},
		{
			name:    "empty",
			in:      "",
			wantErr: true,
		},
		{
			name:    "URL with no db name",
			in:      "mysql://u:p@host:3306/",
			wantErr: true,
		},
		{
			name:    "URL with no host",
			in:      "mysql:///x",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := db.ToDriverDSN(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
