select
	sum(if(txn_stat = 'AUTHEN', quantity, NULL)) as AUTHEN,
	sum(if(txn_stat = 'REJECT', quantity, NULL)) as REJECT,
	sum(if(txn_stat = 'NEW', quantity, NULL)) as NEW,
from (
	select count(*) as quantity, t.txn_status
	from txn_table t
	where
  	1 = 1
  	and t.last_modified >= timestamp(curdate()-1)
  	and t.last_modified < timestamp(curdate())
	group by
  	t.txn_status
) as T;