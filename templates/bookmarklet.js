javascript:(function() {
    var d = document;
    var s = d.createElement("script");
    s.setAttribute("src", "{{ .Url }}");
    d.body.appendChild(s);
}());
