my_numbers = {1,2,3,4,5,6}
odds = for x in *my_numbers
  continue if x % 2 == 1
  x
