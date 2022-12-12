select
	sum(if(txn_status = 'AUTHEN', quantity, NULL)) as AUTHEN,
	sum(if(txn_status = 'REJECT', quantity, NULL)) as REJECT,
	sum(if(txn_status = 'NEW', quantity, NULL)) as NEW,
from (
	select count(*) as quantity, t.txn_status
	from txn_table t
	where t.last_modified >= timestamp(curdate()-1)
  	    and t.last_modified < timestamp(curdate())
	group by t.txn_status
) as T;
