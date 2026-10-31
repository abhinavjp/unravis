function evWhy(id) {
  W.why = id;
  render();
}

function evBack() {
  W.why = null;
  render();
}

function evFind(id) {
  if (typeof SCEN === 'undefined' || !SCEN) {
    return null;
  }
  var keys = ['sql', 'mongo', 'host'];
  for (var i = 0; i < keys.length; i++) {
    var list = SCEN[keys[i]];
    if (Array.isArray(list)) {
      for (var j = 0; j < list.length; j++) {
        var card = list[j];
        if (card && card.id === id) {
          return card;
        }
      }
    }
  }
  return null;
}
