package transport

import _ "embed"

//go:embed admin.html
var adminHTML string

//go:embed register.html
var registerHTML string

//go:embed register.css
var registerCSS string

//go:embed register.js
var registerJS string
