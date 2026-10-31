(function(){
  function pad2(n){ return (n < 10 ? '0' : '') + n; }
  for (var i = 0; i < 52; i++) {
    CALLS.push([
      ['Orders.Create', 'Orders.List', 'Payments.Authorize', 'Basket.Get', 'Catalog.Search'][i % 5],
      '09:' + pad2(10 + (i * 7) % 50) + ':' + pad2((i * 13) % 60),
      2290 - i * 17
    ]);
  }
})();
