javascript:(function() {
    var d = document;
    var s = d.createElement("script");
    s.setAttribute("src", "{{ url }}");
    d.body.appendChild(s);
}());
