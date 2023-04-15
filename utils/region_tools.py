from utils import available_regions

class RegionTool:
    """
        Used to generate URLs for different regions for both Audible and Audnexus.

        Parameters
        ----------
        type : str
            The base type, e.g. 'authors' or 'books'
        id : str, optional
            The ASIN of the item to lookup. Can be None.
        query : str, optional
            Any additional query parameters to add to the URL. Can be None.
            Must be pre-formatted for Audnexus, e.g. '&page=1&limit=10'
        region : str
            The region code to generate the URL for.
    """

    def __init__(self, region: str, content_type: str = 'books', id: str = '', query: str = ''):
        self.region = region
        self.content_type = content_type
        self.id = id
        self.query = query

    # Audnexus
    def get_region_query(self):
        """
            Returns the region query string.
        """
        return '?region=' + self.region

    def get_content_type_url(self):
        """
            Returns the content type URL.
        """
        return 'https://api.audnex.us' + '/' + self.content_type

    # Audible
    def get_api_region_url(self):
        """
            Returns the API region URL.
        """
        return 'https://api.audible.{}'.format(
            available_regions[self.region]['TLD']
        )

    def get_api_params(self):
        """
            Returns the API parameters.
        """
        return (
            '?response_groups=contributors,product_desc,product_attrs'
            '&num_results=25&products_sort_by=Relevance'
        )

    def get_api_search_url(self):
        """
            Returns the API search URL.
        """
        return self.get_api_region_url() + '/' + '1.0/catalog/products' + self.get_api_params() + '&' + self.query
