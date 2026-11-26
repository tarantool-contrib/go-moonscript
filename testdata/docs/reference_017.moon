send_message = ({:sender, :recipient, :body}) ->
  print "#{sender} -> #{recipient}: #{body}"

send_message {
  sender: "leaf"
  recipient: "world"
  body: "hello"
}
