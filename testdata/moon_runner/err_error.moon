-- error() with a string, raised a few calls deep
class Account
  new: (@balance) =>
  withdraw: (n) =>
    if n > @balance
      error "insufficient funds: #{@balance} < #{n}"
    @balance -= n

process = (acct, amounts) ->
  for amt in *amounts
    acct\withdraw amt
  acct.balance

print process Account(100), {10, 20}
print process Account(10), {5, 20}
