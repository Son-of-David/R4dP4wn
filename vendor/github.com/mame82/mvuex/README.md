Partial Vuex wrapper following the interface concept of these Vue bindings https://github.com/HuckRidgeSW/hvue/.

The code supports:
- state
- mutations (provided from native gopherjs methods)
- actions (provided from native gopherjs methods)

The code won't be developed further by myself, so feel free to grab everything.

The example subfolder provides a small example of a Vue.js component rendered to the webpage two times and using a Vuex store as "single source of truth".
It shows how to use actions and mutations (getters aren't implemented).
The example's Vue.js part is done with the use of https://github.com/HuckRidgeSW/hvue (Vue.JS bindings).
The example's Vuex part is implemented using these bindings. 

The whole app is purely written in go with a small index.html container (Vue.js and Vuex.js aren't included, so the client has to be Internet connected to fetch them).

To start the example run:

```
go get -u github.com/mame82/mvuex/example
gopherjs serve
```

... and browse to
`http://localhost:8080/github.com/mame82/mvuex/example/`
